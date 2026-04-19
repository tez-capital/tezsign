#define _DEFAULT_SOURCE

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <dirent.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <errno.h>
#include <pwd.h>

#define GADGET_UDC_FILE "/sys/kernel/config/usb_gadget/g1/UDC"
#define UDC_CLASS_PATH  "/sys/class/udc"
#define FFS_EP1         "/dev/ffs/tezsign/ep1"

// Function to find the first available UDC
char* find_udc() {
    struct dirent *entry;
    DIR *dp = opendir(UDC_CLASS_PATH);
    if (dp == NULL) return NULL;

    while ((entry = readdir(dp))) {
        if (entry->d_name[0] != '.') {
            char *udc_name = strdup(entry->d_name);
            closedir(dp);
            return udc_name;
        }
    }
    closedir(dp);
    return NULL;
}

// Function to change ownership of a file by username
int change_owner(const char *path, const char *user) {
    struct passwd *pw = getpwnam(user);
    if (!pw) {
        fprintf(stderr, "User %s not found\n", user);
        return -1;
    }
    if (chown(path, pw->pw_uid, pw->pw_gid) != 0) {
        perror("chown");
        return -1;
    }
    return 0;
}

int main() {
    // 1. Find UDC
    char *udc = find_udc();
    if (!udc) {
        fprintf(stderr, "No UDC available; cannot attach gadget now.\n");
        return EXIT_FAILURE;
    }

    // 2. Wait for FFS descriptors to be written.
    //    ep1 appears only after ffs_registrar writes both descriptors and
    //    strings to ep0 (FFS transitions to FFS_ACTIVE).  Without this the
    //    UDC bind races with ffs_registrar on warm reboots and fails with
    //    -ENODEV because the FFS function has no descriptors yet.
    printf("Waiting for FFS descriptors...\n");
    while (access(FFS_EP1, F_OK) != 0) {
        usleep(50000); // 50 ms
    }
    printf("FFS descriptors ready.\n");

    // 3. Check if already attached
    char current_udc[256] = {0};
    FILE *fp = fopen(GADGET_UDC_FILE, "r");
    if (fp) {
        if (fgets(current_udc, sizeof(current_udc), fp)) {
            current_udc[strcspn(current_udc, "\n")] = 0;
        }
        fclose(fp);
    }

    if (strlen(current_udc) > 0 && strcmp(current_udc, udc) == 0) {
        printf("UDC is already set to %s.\n", udc);
    } else {
        // 4. Bind gadget to UDC (with retry — DWC3 PHY may still be settling)
        int bound = 0;
        for (int attempt = 0; attempt < 40; attempt++) { // 40 × 250 ms = 10 s
            int fd = open(GADGET_UDC_FILE, O_WRONLY | O_TRUNC);
            if (fd < 0) {
                perror("Failed to open UDC attribute");
                free(udc);
                return EXIT_FAILURE;
            }
            ssize_t n = write(fd, udc, strlen(udc));
            int err = errno;
            close(fd);

            if (n == (ssize_t)strlen(udc)) {
                printf("Attached gadget to UDC: %s\n", udc);
                bound = 1;
                break;
            }
            fprintf(stderr, "UDC bind attempt %d failed: %s\n",
                    attempt + 1, strerror(err));
            usleep(250000); // 250 ms
        }

        if (!bound) {
            fprintf(stderr, "Failed to bind gadget to UDC after retries.\n");
            free(udc);
            return EXIT_FAILURE;
        }
    }

    // 5. Handle soft_connect symlink and ownership
    char target[512], link_path[] = "/tmp/soft_connect";
    snprintf(target, sizeof(target), "/sys/class/udc/%s/soft_connect", udc);
    unlink(link_path);
    if (symlink(target, link_path) == 0) {
        change_owner(link_path, "registrar");
    }

    // 6. Fix endpoint ownership (ep1-ep4 already exist from step 2)
    const char *endpoints[] = {
        "/dev/ffs/tezsign/ep1", "/dev/ffs/tezsign/ep2",
        "/dev/ffs/tezsign/ep3", "/dev/ffs/tezsign/ep4"
    };

    for (int i = 0; i < 4; i++) {
        printf("Waiting for %s...\n", endpoints[i]);
        while (access(endpoints[i], F_OK) != 0) {
            sleep(1);
        }
        if (change_owner(endpoints[i], "tezsign") == 0) {
            printf("Set ownership for %s\n", endpoints[i]);
        }
    }

    printf("All endpoints secured.\n");
    free(udc);
    return EXIT_SUCCESS;
}