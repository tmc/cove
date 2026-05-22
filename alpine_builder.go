package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

const alpineMinirootfsURL = "https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/aarch64/alpine-minirootfs-3.23.4-aarch64.tar.gz"

func installAlpineWithBuilderVM(diskPath string, config LinuxProvisionConfig) error {
	helper := strings.TrimSpace(os.Getenv("COVE_ALPINE_BUILDER_VM"))
	if helper == "" {
		helper = "mlxgo-setup-ubuntu"
	}
	guestWorkDir := strings.TrimSpace(os.Getenv("COVE_ALPINE_BUILDER_GUEST_WORKDIR"))
	if guestWorkDir == "" {
		guestWorkDir = "/mnt/tmcsrc/cove/.tmp/alpine-builder"
	}

	moduleDir, err := findCoveModuleDir()
	if err != nil {
		return err
	}
	hostWorkDir := filepath.Join(moduleDir, ".tmp", "alpine-builder")
	if err := os.MkdirAll(hostWorkDir, 0755); err != nil {
		return fmt.Errorf("create alpine builder work dir: %w", err)
	}

	agentPath := filepath.Join(hostWorkDir, "vz-agent")
	if config.InstallAgent {
		if err := buildAgentBinaryTo(agentPath, "linux", "arm64"); err != nil {
			return fmt.Errorf("build alpine agent: %w", err)
		}
	}

	scriptPath := filepath.Join(hostWorkDir, "build-alpine.sh")
	if err := os.WriteFile(scriptPath, []byte(alpineBuilderScript(guestWorkDir, alpineMinirootfsURL, config)), 0755); err != nil {
		return fmt.Errorf("write alpine builder script: %w", err)
	}

	sock := filepath.Join(vmconfig.Path(helper), "control.sock")
	if !isVMRunning(sock) {
		return fmt.Errorf("alpine builder vm %q is not running\n  start a Linux helper VM with ext4 tools and the cove checkout shared at %s\n  then retry, or set COVE_ALPINE_BUILDER_VM and COVE_ALPINE_BUILDER_GUEST_WORKDIR", helper, guestWorkDir)
	}
	req := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: []string{"sh", filepath.Join(guestWorkDir, "build-alpine.sh")},
			},
		},
	}
	resp, err := ctlSendRequest(sock, req, 15*time.Minute, "agent-exec")
	if err != nil {
		return fmt.Errorf("run alpine builder in %s: %w", helper, err)
	}
	if err := requireAgentExecSuccess("run alpine builder", resp); err != nil {
		return err
	}

	for _, file := range []string{"alpine.img", "vmlinuz", linuxRootUUIDFileName, linuxRootDeviceFileName} {
		src := filepath.Join(hostWorkDir, file)
		info, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("alpine builder did not produce %s: %w", file, err)
		}
		if info.Size() == 0 {
			return fmt.Errorf("alpine builder produced empty %s", file)
		}
	}
	if err := copyFile(filepath.Join(hostWorkDir, "alpine.img"), diskPath); err != nil {
		return fmt.Errorf("copy alpine disk: %w", err)
	}
	if err := copyFile(filepath.Join(hostWorkDir, "vmlinuz"), filepath.Join(vmDir, "vmlinuz")); err != nil {
		return fmt.Errorf("copy alpine kernel: %w", err)
	}
	if err := copyFile(filepath.Join(hostWorkDir, linuxRootUUIDFileName), filepath.Join(vmDir, linuxRootUUIDFileName)); err != nil {
		return fmt.Errorf("copy alpine root uuid: %w", err)
	}
	if err := copyFile(filepath.Join(hostWorkDir, linuxRootDeviceFileName), filepath.Join(vmDir, linuxRootDeviceFileName)); err != nil {
		return fmt.Errorf("copy alpine root device: %w", err)
	}
	if err := writeLinuxInstalledMarker(vmDir, LinuxVariantAlpine); err != nil {
		return fmt.Errorf("write install marker: %w", err)
	}
	return nil
}

func alpineBuilderScript(workDir, minirootfsURL string, config LinuxProvisionConfig) string {
	agentInstall := "true"
	if config.InstallAgent {
		agentInstall = `cp "$WORK/vz-agent" "$ROOT/usr/local/bin/vz-agent"
chmod 755 "$ROOT/usr/local/bin/vz-agent"`
	}
	sshKeyInstall := "true"
	if strings.TrimSpace(config.SSHPubKey) != "" {
		sshKeyInstall = fmt.Sprintf(`mkdir -p "$ROOT/home/%s/.ssh"
printf '%%s\n' %s > "$ROOT/home/%s/.ssh/authorized_keys"
chroot "$ROOT" chown -R %s:%s "/home/%s/.ssh"
chmod 700 "$ROOT/home/%s/.ssh"
chmod 600 "$ROOT/home/%s/.ssh/authorized_keys"`,
			config.Username,
			alpineShellQuote(strings.TrimSpace(config.SSHPubKey)),
			config.Username,
			config.Username, config.Username, config.Username,
			config.Username,
			config.Username)
	}
	return fmt.Sprintf(`#!/bin/sh
set -eu
WORK=%s
IMG=$WORK/alpine.img
ROOT=/tmp/cove-alpine-root
MINI=$WORK/alpine-minirootfs.tar.gz
URL=%s
for tool in losetup parted partprobe mkfs.ext4 wget tar gzip blkid; do
	command -v "$tool" >/dev/null || {
		echo "missing required tool: $tool" >&2
		exit 127
	}
done
rm -f "$IMG"
truncate -s 2G "$IMG"
loop=$(losetup --find --show -P "$IMG")
cleanup() {
	set +e
	for m in "$ROOT/proc" "$ROOT/sys" "$ROOT/dev" "$ROOT"; do
		umount "$m" 2>/dev/null || true
	done
	losetup -d "$loop" 2>/dev/null || true
}
trap cleanup EXIT
parted -s "$loop" mklabel gpt mkpart primary ext4 1MiB 100%%
partprobe "$loop" || true
sleep 1
mkfs.ext4 -F "${loop}p1"
for m in "$ROOT/proc" "$ROOT/sys" "$ROOT/dev" "$ROOT"; do
	umount "$m" 2>/dev/null || true
done
rm -rf "$ROOT"
mkdir -p "$ROOT"
mount "${loop}p1" "$ROOT"
if [ ! -s "$MINI" ]; then
	wget -O "$MINI" "$URL"
fi
tar -xzf "$MINI" -C "$ROOT"
cp /etc/resolv.conf "$ROOT/etc/resolv.conf"
mkdir -p "$ROOT/lib/modules"
cp -a /lib/modules/$(uname -r) "$ROOT/lib/modules/"
%s
mkdir -p "$ROOT/etc/init.d" "$ROOT/etc/runlevels/default" "$ROOT/var/log" "$ROOT/var/lib"
cat > "$ROOT/etc/init.d/vz-agent" <<'EOS'
#!/sbin/openrc-run
name="cove guest agent"
command="/usr/local/bin/vz-agent"
command_args="-mode daemon"
command_background=true
pidfile="/run/vz-agent.pid"
output_log="/var/log/vz-agent.log"
error_log="/var/log/vz-agent.log"
depend() {
	need net
	after firewall
}
start_pre() {
	modprobe vsock >/dev/null 2>&1 || true
	modprobe virtio_vsock >/dev/null 2>&1 || true
	modprobe vmw_vsock_virtio_transport >/dev/null 2>&1 || true
}
EOS
chmod 755 "$ROOT/etc/init.d/vz-agent"
cat > "$ROOT/etc/init.d/dev" <<'EOS'
#!/sbin/openrc-run
description="OpenRC dev dependency"
depend() {
	need devfs
	before hwdrivers machine-id
}
start() {
	return 0
}
EOS
chmod 755 "$ROOT/etc/init.d/dev"
ln -sf /etc/init.d/vz-agent "$ROOT/etc/runlevels/default/vz-agent"
printf '%%s\n' %s > "$ROOT/etc/hostname"
printf '127.0.0.1 localhost\n127.0.1.1 %%s\n' %s > "$ROOT/etc/hosts"
cat > "$ROOT/etc/network/interfaces" <<'EOS'
auto lo
iface lo inet loopback
auto eth0
iface eth0 inet dhcp
EOS
printf 'nameserver 1.1.1.1\n' > "$ROOT/etc/resolv.conf"
touch "$ROOT/var/lib/cove-setup.done"
mount --bind /dev "$ROOT/dev"
mount -t proc proc "$ROOT/proc"
mount -t sysfs sys "$ROOT/sys"
cat > "$ROOT/tmp/cove-finish.sh" <<'EOS'
#!/bin/sh
set -eu
export PATH=/sbin:/bin:/usr/sbin:/usr/bin
apk update
apk add openrc e2fsprogs e2fsprogs-extra dhcpcd openssh sudo ca-certificates
adduser -D -s /bin/ash -G wheel %s
printf '%%s:%%s\n' %s %s | chpasswd
printf '%%s\n' '%%wheel ALL=(ALL) ALL' > /etc/sudoers.d/wheel
chmod 440 /etc/sudoers.d/wheel
for spec in \
	'devfs sysinit' \
	'dev sysinit' \
	'dmesg sysinit' \
	'modules boot' \
	'hostname boot' \
	'bootmisc boot' \
	'sysctl boot' \
	'networking boot' \
	'sshd default' \
	'local default' \
	'vz-agent default'
do
	set -- $spec
	test -x /etc/init.d/$1 && rc-update add $1 $2 || true
done
passwd -d root
EOS
chmod 755 "$ROOT/tmp/cove-finish.sh"
chroot "$ROOT" /bin/sh /tmp/cove-finish.sh
rm -f "$ROOT/tmp/cove-finish.sh"
%s
sync
blkid -s UUID -o value "${loop}p1" > "$WORK/%s"
printf '/dev/vda1\n' > "$WORK/%s"
gzip -dc /boot/vmlinuz-$(uname -r) > "$WORK/vmlinuz"
`,
		alpineShellQuote(workDir),
		alpineShellQuote(minirootfsURL),
		agentInstall,
		alpineShellQuote(config.Hostname),
		alpineShellQuote(config.Hostname),
		alpineShellQuote(config.Username),
		alpineShellQuote(config.Username),
		alpineShellQuote(config.Password),
		sshKeyInstall,
		linuxRootUUIDFileName,
		linuxRootDeviceFileName)
}

func alpineShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
