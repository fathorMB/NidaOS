# NidaOS installer kickstart (feasibility proof, milestone M-01).
# The installer image build replaces the payload marker with the image reference.

text
lang en_US.UTF-8
keyboard us
timezone Europe/Rome --utc
network --bootproto=dhcp --device=link --activate --hostname=nidaos
rootpw --lock

# Install the NidaOS image from the container storage of the installer.
# The installed system gets its updates from the target reference.
bootc --source-imgref containers-storage:@@PAYLOAD_REF@@ --target-imgref @@PAYLOAD_REF@@

# The %pre section writes these two files.
%include /tmp/nida-storage.ks
%include /tmp/nida-users.ks

reboot

%pre --interpreter=/bin/bash --log=/tmp/nida-pre.log
# The two files must exist, or the kickstart cannot include them.
: > /tmp/nida-storage.ks
: > /tmp/nida-users.ks

bash /usr/libexec/nidaos/nida-plan.sh

# The user comes from a seed volume with the label NIDASEED.
# The volume contains one file: authorized_keys.
# Without the seed, the installer asks the operator to make a user.
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

%post --nochroot --log=/tmp/nida-post.log
# Keep the disk plan in /var of the installed system.
# The installer mounts the new root file system. Find it by its ostree directory.
findmnt -rno TARGET | while read -r target; do
    var_dir="$target/ostree/deploy/default/var"
    if [ -d "$var_dir" ]; then
        mkdir -p "$var_dir/lib/nidaos"
        cp /tmp/nida-plan.txt "$var_dir/lib/nidaos/install-plan.txt"
        cp /tmp/nida-pre.log "$var_dir/lib/nidaos/install-pre.log"
        echo "plan saved in $var_dir/lib/nidaos"
        break
    fi
done
%end
