package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/apps"
	"github.com/fathorMB/NidaOS/core/internal/mdns"
)

// appNames gives the names that the mDNS responder announces: the domain
// and the address of each app with a port.
func appNames(store *apps.Store, domain string) []string {
	if domain == "" {
		return nil
	}
	names := []string{domain}
	for _, a := range store.Apps() {
		if a.Config.Port != nil {
			names = append(names, a.Name+"."+domain)
		}
	}
	return names
}

// runMDNSLookup sends an mDNS query and writes the address. It is a check
// for the owner and for the VM test.
func runMDNSLookup(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "use: nida-core mdns-lookup <name>, for example blog.nidaos.local")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addr, err := mdns.Lookup(ctx, args[0], 3*time.Second)
	if err != nil {
		fmt.Fprintf(stderr, "nida-core: no answer for %s: %v\n", args[0], err)
		return 1
	}
	fmt.Fprintln(stdout, addr)
	return 0
}
