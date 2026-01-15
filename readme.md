# tezsign

`tezsign` is a secure, air-gapped signing solution for Tezos consensus operations. It uses a dedicated hardware gadget (like a Raspberry Pi) connected via USB to a host machine, ensuring your keys remain isolated.

## Comparison With Other Available Solutions
| Feature | **TezSign** | **Russignol** | **BLS Signer** |
| :--- | :--- | :--- | :--- |
| **Supported Devices** | 🥧 RPi Zero 2W, RPi4, Radxa Zero 3 | 🥧 RPi Zero 2W w/ PaperInk | 🥧 RPi Zero 2W w/ PaperInk |
| **Hardware Start Cost** | **< $20 USD** * | **~$50 USD** * | **~$50 USD** * |
| **Tezbake Integration** | Full | Partial | Partial |
| **Avg Signature Time** | ~10ms | ~5ms | ~30ms |
| **Security** | 🔒 **High** (Custom Wire Proto) | 🔒 **High** (Custom Image) | 🛡️ Medium |
| **Power Loss Safe** | ✅ **Yes** | ✅ **Yes** | ⚠️ No |
| **Boot Time** | ⏱️ 15s | 🚀 **5s** | ~1.5m |
| **Multi-Device Support**| ✅ **Yes** | ❌ No | ❌ No |
| **Multi-Baker Support** | ✅ **Yes** | ❌ No | ❌ No |
| **Companion App** | Required | Optional | No |
| **Physical Pinlock** | ❌ No (App-based) | 👆 Yes (Touch Screen) | 👆 Yes (Touch Screen) |
| **Auto Unlock on Boot**| ✅ **Yes** (Optional) | ❌ No | ❌ No |
| **Compressed Image Size** | 💾 ~200MB | 📦 7MB | 🐘 1.95GB |
| **License** | 📜 SSPL | ⛔ UNLICENSED | 📜 MIT |

> **Note:** The comparison table above is accurate as of December 2, 2025.

> **Disclaimer:** The values for other signers in the comparison table are provided by their respective providers or users and have not been independently verified by us. If you notice any inaccuracies, please [let us know](https://github.com/tez-capital/tezsign/issues).

> **\*** * Shipping & taxes may apply.
>
> **Note on Power Loss:** "Yes" indicates the device is hardened against corruption if power is suddenly cut.

## 🚀 Get Started

### What you need:

* **Hardware Gadget:** Radxa Zero 3 or any Raspberry Pi Zero 2W, 4, or other models with an OTG USB port. Note that the Raspberry Pi 5 is *not* recommended.
* **SD Card:** 4GB or larger. A high-quality, industrial-grade/endurance SD card is **highly recommended**.

> **NOTE:** There is a known issue with the Raspberry Pi DWC2 USB driver that can cause USB stack failures. We have implemented a workaround to mitigate this, which can be found in our [DWC2 patch](httpss://github.com/tez-capital/tezsign/blob/main/armbian_userpatches/kernel/archive/bcm2711-6.12/kernel-bcm2711-current.patch). This is not an operational issue in itself, but you should be aware that the Linux kernel has been amended to address this.

---

## 🏛️ Architecture

`tezsign` consists of two parts:

* **Gadget:** The external, air-gapped device connected to the host over USB, acting as a peripheral. This is where your keys live and signing operations happen.
* **Host App:** Your companion application (the `tezsign` command-line tool) which you use to control the gadget from your host machine.

> **Note:** If you want to run `tezsign` as a standalone service (not in conjunction with `tezbake`) on Linux or macOS, please refer to the [AMI Guide](https://github.com/tez-capital/tezsign/blob/main/readme.ami.md) for detailed instructions.

---

## ⚙️ Setup

1.  Download the **gadget image** for your specific device and the **host app**.
    - [tezsign Releases](https://github.com/tez-capital/tezsign/releases)  
    - **IMPORTANT:** For production use, avoid images with `dev` in their name.
2.  Use Balena Etcher (or a tool you are familiar with) to flash the gadget image to your SD card.
3.  Plug the SD card into your board (e.g., Radxa Zero 3, RPi Zero 2W).
4.  Connect the board to your host machine.
    * **Important:** Make sure you use a good quality USB cable and connect it to the **OTG port** of your board.
5.  **(Linux Hosts Only) Add udev rules:**

    > **Note:** *If you are installing with `tezbake` or using the AMI, you do not need to install udev rules manually. Both `tezbake` and the AMI handle this automatically during `setup-tezsign`.*

    To allow your host machine to communicate with the gadget without root privileges, you need to add a `udev` rule. Run the helper script (it writes `/etc/udev/rules.d/99-tezsign.rules` and reloads `udev`) to install the required rule:
    ```bash
    sudo ./tools/add_udev_rules.sh
    ```
    After running the script, make sure your user is part of the `plugdev` group:
    ```bash
    sudo usermod -aG plugdev $USER
    ```
    You will need to log out and log back in for this group change to take effect.


After the initial connection, the device will configure itself and reboot. This process takes approximately 30 seconds.

> **NOTE:** The Radxa Zero 3 may encounter an issue where it fails to boot correctly after the initial configuration. If this occurs, wait until the LED diode stops blinking (indicating the configuration is complete), then unplug and reconnect the device. This issue appears to be related to certain SD cards, as some exhibit this behavior while others do not.

---

## ✨ Initialization & Usage

After about 30 seconds, your device should be ready. It's time to initialize it.

Assuming your host app is available in your path as `tezsign`:

1.  **Confirm Device Connection**
    ```bash
    ./tezsign list-devices
    ./tezsign version
    ```

2.  **Initialize the Device**
    This prompts you for a master password.
    ```bash
    ./tezsign init
    ```
    > **Warning:** It is not currently possible to change this password. Please choose wisely!

3.  **Generate New Keys**
    Generate the keys you need, giving them descriptive aliases.
    ```bash
    ./tezsign new consensus companion
    ```
    *(You can use any aliases you like, not just "consensus" and "companion".)*

4.  **List Keys & Check Status**
    You can list all available keys on the device and check their status.
    ```bash
    ./tezsign list
    ./tezsign status
    ```

5.  **Register Keys On-Chain**
    To register your keys on the Tezos network, you will need their public key (`BLpk`) and a proof of possession. You can get these details using:
    ```bash
    ./tezsign status --full
    ```
    Use the `BLpk` and proof of possession to register the keys as a consensus or companion key. You can use a tool like [tezgov](https://gov.tez.capital/) to do this comfortably.

6.  **Unlock Keys & Run Signer**
    After the keys are registered on-chain, you must unlock them on the device to allow them to sign operations.
    ```bash
    ./tezsign unlock consensus companion
    ```
    *(Use the same aliases you created in step 3.)*

7.  **Start the Signer Server**
    Finally, start the signer server. Your baker should be configured to point to this address and port.
    ```bash
    ./tezsign run --listen 127.0.0.1:20090
    ```
    At this point, `tezsign` is ready for baking. Make sure your baker points to it when the registered keys activate, and it will sign baking operations automatically.

---

## 🔒 Security

See [security.md](https://github.com/tez-capital/tezsign/blob/main/security.md)

---

## 🛠️ Development

See [readme.dev.md](https://github.com/tez-capital/tezsign/blob/main/readme.dev.md)
