#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <errno.h>
#include <stdarg.h>

#define GADGET_BASE   "/sys/kernel/config/usb_gadget/g1"
#define MAC_ADDR      "ae:d3:e6:cd:ff:f2"
#define HOST_MAC_ADDR "ae:d3:e6:cd:ff:f3"

/* --- Helper Functions --- */

int mkdir_p(const char *path) {
    char tmp[256];
    char *p = NULL;
    size_t len;

    snprintf(tmp, sizeof(tmp), "%s", path);
    len = strlen(tmp);
    if (tmp[len - 1] == '/')
        tmp[len - 1] = 0;

    for (p = tmp + 1; *p; p++) {
        if (*p == '/') {
            *p = 0;
            if (mkdir(tmp, 0755) != 0 && errno != EEXIST) {
                return -1;
            }
            *p = '/';
        }
    }

    if (mkdir(tmp, 0755) != 0 && errno != EEXIST) {
        return -1;
    }
    return 0;
}

// Writes formatted strings to sysfs/configfs attributes
int write_attr(const char *path, const char *fmt, ...) {
    FILE *fp = fopen(path, "w");
    if (!fp) {
        perror(path);
        return -1;
    }

    va_list args;
    va_start(args, fmt);
    if (vfprintf(fp, fmt, args) < 0) {
        va_end(args);
        fclose(fp);
        return -1;
    }
    va_end(args);

    fclose(fp);
    return 0;
}

/* --- Logic Functions --- */

int setup_ecm_function() {
    const char *ecm_func_dir = GADGET_BASE "/functions/ecm.usb0";
    const char *conf_dir = GADGET_BASE "/configs/c.1/ecm.usb0";

    printf("Step 1: Creating ECM function directory...\n");
    if (mkdir_p(ecm_func_dir) != 0) {
        fprintf(stderr, "Error: Failed to create %s: %s\n", ecm_func_dir, strerror(errno));
        return -1;
    }

    printf("Step 2: Setting MAC Addresses...\n");
    if (write_attr(GADGET_BASE "/functions/ecm.usb0/dev_addr", "%s", MAC_ADDR) != 0) return -1;
    if (write_attr(GADGET_BASE "/functions/ecm.usb0/host_addr", "%s", HOST_MAC_ADDR) != 0) return -1;

    printf("Step 3: Linking ECM function to configuration...\n");
    if (symlink(ecm_func_dir, conf_dir) != 0) {
        if (errno == EEXIST) {
            printf("Notice: ECM function already linked.\n");
        } else {
            fprintf(stderr, "Error: Failed to link: %s\n", strerror(errno));
            return -1;
        }
    }

    return 0;
}

int main(int argc, char *argv[]) {
    // Check for root privileges as configfs usually requires them
    if (geteuid() != 0) {
        fprintf(stderr, "This program must be run as root (sudo).\n");
        return EXIT_FAILURE;
    }

    printf("Starting USB Gadget ECM Setup...\n");

    if (setup_ecm_function() == 0) {
        printf("ECM setup completed successfully.\n");

        // --- NEW: Safely generate Dropbear Host Keys ---
        
        // 1. Ensure the /etc/dropbear directory exists
        struct stat st = {0};
        if (stat("/etc/dropbear", &st) == -1) {
            mkdir("/etc/dropbear", 0755);
        }

        // 2. Check for RSA key, generate if missing
        if (access("/etc/dropbear/dropbear_rsa_host_key", F_OK) != 0) {
            printf("Generating Dropbear RSA host key (this may take a moment)...\n");
            // Redirecting output to /dev/null keeps your boot logs clean
            system("dropbearkey -t rsa -f /etc/dropbear/dropbear_rsa_host_key > /dev/null 2>&1");
        }

        // 3. Check for Ed25519 key, generate if missing
        if (access("/etc/dropbear/dropbear_ed25519_host_key", F_OK) != 0) {
            printf("Generating Dropbear Ed25519 host key...\n");
            system("dropbearkey -t ed25519 -f /etc/dropbear/dropbear_ed25519_host_key > /dev/null 2>&1");
        }
        
        printf("Dropbear keys are ready.\n");
        // -----------------------------------------------

        return EXIT_SUCCESS;
    }
}