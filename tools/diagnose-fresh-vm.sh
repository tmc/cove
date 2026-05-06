#!/usr/bin/env bash
set -euo pipefail

usage() {
	echo "usage: $0 <vm-name-or-dir>" >&2
	exit 2
}

if [[ $# -ne 1 ]]; then
	usage
fi

vm=$1
if [[ -d "$vm" ]]; then
	vm_dir=$vm
else
	vm_dir="${HOME}/.vz/vms/${vm}"
fi
disk="${vm_dir}/disk.img"
if [[ ! -f "$disk" ]]; then
	echo "disk image not found: $disk" >&2
	exit 1
fi

device=
mount_point=
cleanup() {
	if [[ -n "${mount_point}" && -d "${mount_point}" ]]; then
		diskutil unmount "${mount_point}" >/dev/null 2>&1 || true
		rmdir "${mount_point}" >/dev/null 2>&1 || true
	fi
	if [[ -n "${device}" ]]; then
		hdiutil detach "${device}" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

device=$(hdiutil attach "$disk" -readonly -nobrowse -nomount | awk 'NR == 1 {print $1}')
if [[ -z "${device}" ]]; then
	echo "could not attach disk image" >&2
	exit 1
fi

data_part=$(
	diskutil apfs list |
		awk -v dev="$(basename "${device}")" '
			$0 ~ "Physical Store " dev "s" { in_container = 1 }
			in_container && /APFS Volume Disk \(Role\):/ && /\(Data\)/ {
				for (i = 1; i <= NF; i++) {
					if ($i ~ /^disk[0-9]+s[0-9]+$/) {
						print $i
						exit
					}
				}
				exit
			}
		'
)
if [[ -z "${data_part}" ]]; then
	echo "could not find APFS Data volume for ${device}" >&2
	diskutil list "${device}" >&2
	diskutil apfs list >&2 || true
	exit 1
fi

mount_point=$(mktemp -d /tmp/cove-fresh-vm.XXXXXX)
diskutil mount readOnly -mountOptions owners -mountPoint "${mount_point}" "${data_part}" >/dev/null

check_stat() {
	local path=$1
	if [[ -e "${mount_point}/${path}" ]]; then
		stat -f '%Sp %u:%g %N' "${mount_point}/${path}"
	else
		echo "missing ${path}"
	fi
}

echo "VM: ${vm_dir}"
echo "Disk: ${disk}"
echo "Device: ${device}"
echo "Data volume: ${data_part}"
echo
echo "Ownership:"
check_stat "Library/LaunchDaemons/com.github.tmc.vz-macos.provision.plist"
check_stat "private/var/db/vz-provision.sh"
check_stat "private/etc/kcpassword"
check_stat "Library/Preferences/com.apple.loginwindow.plist"
check_stat "usr/local/bin/vz-agent"
check_stat "Library/LaunchDaemons/com.github.tmc.vz-macos.vz-agent.plist"
echo
echo "Provisioning markers:"
check_stat "private/var/log/vz-provision.log"
check_stat "private/var/db/.vz-provisioned"
echo
echo "Provisioning LaunchDaemon:"
if [[ -f "${mount_point}/Library/LaunchDaemons/com.github.tmc.vz-macos.provision.plist" ]]; then
	plutil -p "${mount_point}/Library/LaunchDaemons/com.github.tmc.vz-macos.provision.plist"
fi
echo
echo "Auto-login preferences:"
if [[ -f "${mount_point}/Library/Preferences/com.apple.loginwindow.plist" ]]; then
	plutil -p "${mount_point}/Library/Preferences/com.apple.loginwindow.plist"
fi
