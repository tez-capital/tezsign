# 🔒 tezsign Security Policy

The security of `tezsign` is a top priority. This document details the security measures implemented at both the system and application levels to protect your keys.

## 🛡️ Security Features

### System-Level Security

* **Minimal OS:** Uses a minimal Yocto image to reduce the attack surface.
* **Disabled Wireless Connectivity:** To maintain a strict air-gap, wireless drivers are removed (Radxa), or system overlays are used to disable Wi-Fi and Bluetooth (RPi). Note that the overlay method does not currently work on the RPi 5.
* **Immutable File System:** The `bootfs`, `rootfs`, and `app` partitions are mounted as **read-only**.
* **Secure Data Partition:** A separate `data` partition for application data is mounted as **read-write** but **non-executable**.
* **Offline Updates:** Updates cannot be performed while the device is operating. They are meant to be done directly by re-flashing the SD card.
* **Principle of Least Privilege:**
    * The `tezsign` application runs as a **non-privileged user**.
    * It operates via FunctionFS, with the USB interface endpoint on the gadget side running under a separate, non-privileged user.
* **Locked Accounts:** All user accounts on the system are disabled.*

> ***Warning:** "Dev" images have a `dev` account enabled. Please do not use these images in production unless you know exactly what you are doing.

### Application-Level (Signer) Security

* **Scoped Operations:** The custom signer is designed to sign **only** Tezos consensus operations.
* **Encryption at Rest:** All keys and related sensitive data are encrypted (even at runtime).
* **Double-Signing Protection:** Implements a High Watermark (HWM) to prevent double-signing. This HWM cannot be lowered, even by the operator.*

## ❗ Physical Security Disclaimer

> **NOTE:** The security measures listed above are effective while the device is running. An attacker with physical access can unplug the SD card, mount it on another machine, and edit its contents. Always ensure the physical security of your `tezsign` gadget.

## 🎁 Bounties

Please see the issues tagged `bounty` in our repository or reach out directly for more information on available bounties.

## Reporting a Vulnerability

Please report any security vulnerabilities to **support@tez.capital** with the subject line:

`TEZSIGN SEC: <name of the issue>`

We will work to resolve the issue with the fastest possible priority.
