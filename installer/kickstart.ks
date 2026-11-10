# NidaOS installer kickstart (feasibility proof, milestone M-01).
# scripts/make-installer-config.py puts installer/nida-plan.sh in the %pre section.
# bootc-image-builder adds the command that installs the container image.

text
lang en_US.UTF-8
keyboard us
timezone Europe/Rome --utc
network --bootproto=dhcp --device=link --activate --hostname=nidaos
rootpw --lock

# The %pre section writes these two files.
%include /tmp/nida-storage.ks
%include /tmp/nida-users.ks

reboot

%pre --interpreter=/bin/bash --log=/tmp/nida-pre.log
@@NIDA_PLAN@@

# The user comes from a seed volume with the label NIDASEED.
# The volume contains one file: authorized_keys.
# Without the seed, the installer asks the operator to make a user.
: > /tmp/nida-users.ks
nida_seed=$(blkid -L NIDASEED 2>/dev/null || true)
if [ -n "$nida_seed" ]; then
    mkdir -p /tmp/nida-seed
    if mount -o ro "$nida_seed" /tmp/nida-seed; then
        nida_key=$(grep -m1 -E '^(ssh-|ecdsa-|sk-)' /tmp/nida-seed/authorized_keys 2>/dev/null || true)
        umount /tmp/nida-seed
        if [ -n "$nida_key" ]; then
            {
                echo "user --name=nida --groups=wheel --lock"
                echo "sshkey --username=nida \"$nida_key\""
            } > /tmp/nida-users.ks
        fi
    fi
fi
%end

%post --nochroot
mkdir -p /mnt/sysimage/etc/nidaos
cp /tmp/nida-plan.txt /mnt/sysimage/etc/nidaos/install-plan.txt || true
cp /tmp/nida-pre.log /mnt/sysimage/etc/nidaos/install-pre.log || true
%end
