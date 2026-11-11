package installer

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Facts describe the computer for the welcome step and the network step.
type Facts struct {
	Computer string `json:"computer"`
	CPU      string `json:"cpu"`
	Threads  int    `json:"threads"`
	MemoryB  int64  `json:"memory_bytes"`
	// Firmware is "UEFI" or "BIOS".
	Firmware string  `json:"firmware"`
	Network  []NIC   `json:"network"`
	Version  string  `json:"version"`
}

// NIC is a network interface with a cable or a wireless link.
type NIC struct {
	Name      string   `json:"name"`
	Driver    string   `json:"driver,omitempty"`
	SpeedMbit int      `json:"speed_mbit,omitempty"`
	Carrier   bool     `json:"carrier"`
	Addresses []string `json:"addresses,omitempty"`
	Wireless  bool     `json:"wireless,omitempty"`
}

// ReadFacts reads /sys and /proc below root ("/" on the computer).
func ReadFacts(root string) Facts {
	read := func(p string) string {
		b, _ := os.ReadFile(filepath.Join(root, p))
		return strings.TrimSpace(string(b))
	}
	f := Facts{Firmware: "BIOS"}
	if _, err := os.Stat(filepath.Join(root, "sys/firmware/efi")); err == nil {
		f.Firmware = "UEFI"
	}
	vendor, board, product := read("sys/class/dmi/id/board_vendor"), read("sys/class/dmi/id/board_name"), read("sys/class/dmi/id/product_name")
	switch {
	case board != "" && !placeholder(board):
		f.Computer = strings.TrimSpace(shortVendor(vendor) + " " + board)
	case product != "" && !placeholder(product):
		f.Computer = strings.TrimSpace(shortVendor(read("sys/class/dmi/id/sys_vendor")) + " " + product)
	default:
		f.Computer = "This computer"
	}
	if b, err := os.ReadFile(filepath.Join(root, "proc/cpuinfo")); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), ":")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "model name":
				if f.CPU == "" {
					f.CPU = cleanCPU(strings.TrimSpace(v))
				}
			case "processor":
				f.Threads++
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, "proc/meminfo")); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) >= 2 && fs[0] == "MemTotal:" {
				kb, _ := strconv.ParseInt(fs[1], 10, 64)
				f.MemoryB = kb * 1024
			}
		}
	}
	f.Network = readNICs(root)
	return f
}

func placeholder(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "to be filled") || strings.Contains(l, "default string") || l == "none" || l == "not applicable"
}

func shortVendor(v string) string {
	for _, cut := range []string{" Co., Ltd.", " Co.,Ltd.", " Corporation", " Inc.", " Computer INC.", " International", " Technology"} {
		v = strings.ReplaceAll(v, cut, "")
	}
	if strings.HasPrefix(v, "Micro-Star") {
		return "MSI"
	}
	if strings.HasPrefix(v, "ASUSTeK") {
		return "ASUS"
	}
	return strings.TrimSpace(v)
}

func cleanCPU(s string) string {
	for _, cut := range []string{"(R)", "(TM)", "(tm)", " CPU", " Processor"} {
		s = strings.ReplaceAll(s, cut, "")
	}
	if i := strings.Index(s, " @ "); i > 0 {
		s = s[:i]
	}
	return strings.Join(strings.Fields(s), " ")
}

// readNICs gives the physical network interfaces. The addresses come
// from the network stack, thus only for root "/".
func readNICs(root string) []NIC {
	dirs, _ := filepath.Glob(filepath.Join(root, "sys/class/net/*"))
	var out []NIC
	for _, d := range dirs {
		name := filepath.Base(d)
		if _, err := os.Stat(filepath.Join(d, "device")); err != nil {
			continue // lo, bridges and virtual interfaces have no device
		}
		n := NIC{Name: name}
		if l, err := os.Readlink(filepath.Join(d, "device", "driver")); err == nil {
			n.Driver = filepath.Base(l)
		}
		if b, err := os.ReadFile(filepath.Join(d, "carrier")); err == nil {
			n.Carrier = strings.TrimSpace(string(b)) == "1"
		}
		if b, err := os.ReadFile(filepath.Join(d, "speed")); err == nil {
			if s, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && s > 0 {
				n.SpeedMbit = s
			}
		}
		if _, err := os.Stat(filepath.Join(d, "wireless")); err == nil {
			n.Wireless = true
		}
		if root == "/" {
			if ifi, err := net.InterfaceByName(name); err == nil {
				addrs, _ := ifi.Addrs()
				for _, a := range addrs {
					if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil {
						n.Addresses = append(n.Addresses, ip.String())
					}
				}
			}
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Carrier != out[j].Carrier {
			return out[i].Carrier
		}
		return out[i].Name < out[j].Name
	})
	return out
}
