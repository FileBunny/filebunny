#!/usr/bin/env bash
# A local Debian 13 VM (libvirt/QEMU) to test deploy/ansible and
# scripts/deploy.sh against, sized like a small VPS (1 vCPU, 1 GiB).
#
#   scripts/test-vm.sh up        create and start it, print its address
#   scripts/test-vm.sh down      remove it and its disks
#
# Needs libvirt (qemu:///system, the "default" NAT network), virt-install
# and cloud-localds (cloud-image-utils). The VM gets the official Debian
# cloud image, checked against Debian's SHA512SUMS (fetched over HTTPS;
# Debian does not sign the cloud image sums). cloud-init creates the user
# "debian" with a throwaway SSH key in the cache directory; the playbook
# then sets up the real admin user. Test data only: never real keys.
set -euo pipefail

name=filebunny-test
url=https://cloud.debian.org/images/cloud/trixie/latest
image=debian-13-genericcloud-amd64.qcow2
cache=${XDG_CACHE_HOME:-$HOME/.cache}/filebunny-test-vm
export LIBVIRT_DEFAULT_URI=qemu:///system

up() {
	mkdir -p "$cache"
	cd "$cache"
	curl -fsS -o SHA512SUMS "$url/SHA512SUMS"
	if ! grep " $image\$" SHA512SUMS | sha512sum -c --quiet 2>/dev/null; then
		curl -fsSL -o "$image" "$url/$image"
		grep " $image\$" SHA512SUMS | sha512sum -c --quiet
	fi
	[[ -f id_ed25519 ]] || ssh-keygen -q -t ed25519 -N "" -C "$name (test only)" -f id_ed25519
	cat >user-data <<EOF
#cloud-config
hostname: $name
users:
  - name: debian
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_authorized_keys:
      - $(cat id_ed25519.pub)
EOF
	cloud-localds seed.iso user-data

	virsh net-list --name | grep -qx default || virsh net-start default >/dev/null
	if ! virsh pool-list --all --name | grep -qx default; then
		virsh pool-define-as default dir --target /var/lib/libvirt/images >/dev/null
		virsh pool-build default >/dev/null
	fi
	virsh pool-list --name | grep -qx default || virsh pool-start default >/dev/null
	virsh dominfo "$name" >/dev/null 2>&1 && { echo "$name exists; run: $0 down" >&2; exit 1; }

	upload "$image" "$name.qcow2" qcow2
	virsh vol-resize "$name.qcow2" 25G --pool default >/dev/null # like the VPS disk
	upload seed.iso "$name-seed.iso" raw

	virt-install --name "$name" --memory 1024 --vcpus 1 --cpu host-passthrough --import \
		--osinfo debian12 \
		--disk vol=default/"$name.qcow2",bus=virtio \
		--disk vol=default/"$name-seed.iso",device=cdrom \
		--network network=default,model=virtio \
		--graphics none --video vga --noautoconsole >/dev/null # without a video device the image's GRUB resets in a loop

	echo -n "waiting for an address"
	for _ in $(seq 60); do
		for source in lease arp; do
			ip=$(virsh domifaddr "$name" --source "$source" 2>/dev/null | awk '/ipv4/ {sub(/\/.*/, "", $4); print $4; exit}')
			[[ -n $ip ]] && break 2
		done
		echo -n .
		sleep 2
	done
	echo
	[[ -n ${ip:-} ]] || { echo "no address; see: virsh console $name" >&2; exit 1; }
	echo "$name: $ip"
	echo "ssh -i $cache/id_ed25519 -o UserKnownHostsFile=$cache/known_hosts debian@$ip"
}

# Copies a local file into the default pool as a volume.
upload() {
	local size
	size=$(stat -c %s "$1")
	virsh vol-create-as default "$2" "$size" --format "$3" >/dev/null
	virsh vol-upload "$2" "$1" --pool default
}

down() {
	virsh destroy "$name" >/dev/null 2>&1 || true
	virsh undefine "$name" --remove-all-storage >/dev/null 2>&1 || true
	for v in "$name.qcow2" "$name-seed.iso"; do virsh vol-delete "$v" --pool default >/dev/null 2>&1 || true; done
	rm -f "$cache/known_hosts"
	echo "$name removed"
}

case ${1:-} in
up) up ;;
down) down ;;
*) echo "usage: $0 up|down" >&2; exit 2 ;;
esac
