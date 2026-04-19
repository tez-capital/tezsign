# tezsign Developer Guide

This document provides instructions for developers who want to build, troubleshoot, or contribute to `tezsign`.

## ❗ Developer (`dev`) Images

For development and debugging, `tezsign` provides special `dev` flavor images. These images are **insecure by design** and **must never be used in a production environment**.

### `dev` Image Features

`dev` images are built on top of the production images but include several key additions to facilitate development:

1.  **`dev` User Account:** A `dev` account is created with the password `tezsign`.
2.  **Sudo Access:** The `dev` user has full `sudoers` permissions, allowing root access.
3.  **SSH Server:** The image automatically starts an SSH server on boot.
4.  **ECM Gadget:** In addition to the standard `tezsign` USB gadget, the `dev` image enables an **ECM (Ethernet Control Model) gadget**. This creates a USB Ethernet interface, allowing you to SSH into the device from your host machine.

### Host Machine Setup (Linux)

For your host machine to recognize and configure the USB Ethernet (ECM) gadget, run the helper script that installs the required `udev` rules. This allows your host to automatically assign an IP address to its side of the USB connection, enabling you to reach the `tezsign` gadget.

```bash
sudo ./tools/add_dev_udev_rules.sh
```

The script writes:

* `/etc/udev/rules.d/50-usb-gadget.rules` — assigns the `tezsign_dev` name to the interface with MAC `ae:d3:e6:cd:ff:f3`.
* `/etc/udev/rules.d/99-usb-network.rules` — configures the host side with `10.10.10.2/24` and brings the interface up.

It also reloads the `udev` rules so you do not have to run additional commands manually. (The gadget image is already configured with `10.10.10.1`.)

### Accessing the Gadget

Once your `udev` rules are in place and the `tezsign` gadget is connected, you can SSH into it from your host machine:

```bash
ssh dev@10.10.10.1
```

The password is: `tezsign`

You will now have a full shell on the `tezsign` gadget with `sudo` access, allowing you to inspect logs, test services, and debug the application.

### Running Benchmarks

Benchmarks are run from the host machine, not from the SSH shell on the gadget. Keep the gadget connected over USB, then run the benchmark command from the repository root:

```bash
go run ./app/tests/benchmark -n 1000 -warmup 50 -kind all
```

The command prompts for the master passphrase unless you provide it with `-pass` or `TEZSIGN_BENCH_PASS`:

```bash
TEZSIGN_BENCH_PASS='your-passphrase' go run ./app/tests/benchmark -n 1000 -warmup 50 -kind all
```

For a real-life two-key pattern, use `-mode real-life`. This uses the same two keys for every cycle: generate one payload, sign it with key 1, sign the same payload with key 2, sleep, then repeat with the next payload.

```bash
go run ./app/tests/benchmark -mode real-life -kind attestation -real-life-pairs 10 -real-life-interval 3s
```

`-real-life-pairs 10` means 10 cycles with the same 2 keys, so 20 sign requests total. To reuse specific existing keys instead of creating temporary benchmark keys:

```bash
go run ./app/tests/benchmark -mode real-life -kind attestation -key key-a -key2 key-b -cleanup=false
```

### Working with the Read-Only Filesystem

By default, all partitions on the device (except for `/data`) are mounted as **read-only** for security. Partition layouts may differ between devices. You can inspect all current mount points and their state (like `ro` for read-only) by running:

```bash
mount | grep " ro,"
```

If you need to make changes to a read-only partition (e.g., to modify a file on the root filesystem), you must first remount it as read-write.

Use the following command template:
```bash
sudo mount -o remount,rw <path-to-mount-point>
```

For example, to make the root filesystem (`/`) read-write, run:
```bash
sudo mount -o remount,rw /
```
You can now edit files on the root partition.

Once you are finished with your changes, the easiest way to return the device to its read-only state is to simply **reboot** it.
```bash
sudo reboot
```


### Yocto Builds

Local image builds now use KAS and Yocto directly. See `kas/readme.md` for the production and dev build commands for Raspberry Pi 4, Raspberry Pi Zero 2 W, and Radxa Zero 3W.
