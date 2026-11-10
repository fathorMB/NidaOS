#!/bin/bash
# NidaOS installer disk plan (feasibility proof, milestone M-01).
#
# The script examines the disks and writes two files:
#   - a kickstart fragment with the partitions;
#   - a text file with the plan, for the operator and for the tests.
#
# Rules:
#   1. Ignore removable disks, read-only disks, USB disks and small disks.
#   2. Speed class: NVMe is 0, other solid-state disks are 1, hard disks are 2.
#   3. Sort by class, then by size (the smallest first), then by name.
#   4. The first disk gets the operating system.
#   5. The next solid-state disk is the "performance" tier.
#   6. The first hard disk is the "standard" tier.
#   7. The script does not touch the other disks.
#
# The real planner will measure the speed of the disks. This proof does not.
#
# The %pre section of the kickstart runs this script in the installer.

NIDA_MIN_BYTES=${NIDA_MIN_BYTES:-$((8 * 1024 * 1024 * 1024))}
NIDA_STORAGE_KS=${NIDA_STORAGE_KS:-/tmp/nida-storage.ks}
NIDA_PLAN_FILE=${NIDA_PLAN_FILE:-/tmp/nida-plan.txt}

# Print one line for each candidate disk: "<class> <size> <name>".
nida_list_disks() {
    lsblk -dnb -o NAME,SIZE,ROTA,RM,RO,TYPE,TRAN | while read -r name size rota rm ro type tran; do
        [ "$type" = "disk" ] || continue
        [ "$rm" = "0" ] || continue
        [ "$ro" = "0" ] || continue
        [ "$tran" = "usb" ] && continue
        case "$name" in zram*|loop*|ram*) continue ;; esac
        [ "$size" -ge "$NIDA_MIN_BYTES" ] || continue
        if [ "$tran" = "nvme" ]; then
            class=0
        elif [ "$rota" = "0" ]; then
            class=1
        else
            class=2
        fi
        echo "$class $size $name"
    done | sort -k1,1n -k2,2n -k3,3
}

nida_plan() {
    local disks os="" perf="" std="" unused="" class size name used

    # The two files must exist, or the kickstart cannot include them.
    : > "$NIDA_STORAGE_KS"
    : > "$NIDA_PLAN_FILE"

    disks=$(nida_list_disks)
    if [ -z "$disks" ]; then
        echo "error=no-candidate-disk" >> "$NIDA_PLAN_FILE"
        return 0
    fi

    while read -r class size name; do
        if [ -z "$os" ]; then
            os=$name
        elif [ -z "$perf" ] && [ "$class" -lt 2 ]; then
            perf=$name
        elif [ -z "$std" ] && [ "$class" -eq 2 ]; then
            std=$name
        else
            unused="$unused $name"
        fi
    done <<< "$disks"

    {
        echo "os=$os"
        echo "performance=$perf"
        echo "standard=$std"
        echo "unused=${unused# }"
        echo "# class size name"
        echo "$disks" | sed 's/^/# /'
    } >> "$NIDA_PLAN_FILE"

    used=$os
    [ -n "$perf" ] && used="$used,$perf"
    [ -n "$std" ] && used="$used,$std"

    {
        echo "ignoredisk --only-use=$used"
        echo "zerombr"
        echo "clearpart --all --initlabel --drives=$used"
        echo "bootloader --boot-drive=$os"
        if [ -d /sys/firmware/efi ]; then
            echo "part /boot/efi --fstype=efi --size=600 --ondisk=$os"
        else
            echo "part biosboot --fstype=biosboot --size=1 --ondisk=$os"
        fi
        echo "part /boot --fstype=ext4 --size=1024 --ondisk=$os"
        echo "part / --fstype=xfs --size=8192 --grow --ondisk=$os"
        if [ -n "$perf" ]; then
            echo "part /var/nida/performance --fstype=xfs --size=1024 --grow --ondisk=$perf --label=nida-perf"
        fi
        if [ -n "$std" ]; then
            echo "part /var/nida/standard --fstype=xfs --size=1024 --grow --ondisk=$std --label=nida-std"
        fi
    } >> "$NIDA_STORAGE_KS"
}

nida_plan
