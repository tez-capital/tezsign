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

    // 2. Check if already attached
    char current_udc[256] = {0};
    FILE *fp = fopen(GADGET_UDC_FILE, "r");
    if (fp) {
        if (fgets(current_udc, sizeof(current_udc), fp)) {
            current_udc[strcspn(current_udc, "\n")] = 0; // trim newline
        }
        fclose(fp);
    }

    if (strcmp(current_udc, udc) == 0) {
        printf("UDC is already set to %s.\n", udc);
    } else {
        // 3. Attach Gadget
        FILE *wfp = fopen(GADGET_UDC_FILE, "w");
        if (!wfp) {
            perror("Failed to open UDC attribute");
            free(udc);
            return EXIT_FAILURE;
        }
        fprintf(wfp, "%s", udc);
        fclose(wfp);
        printf("Attached gadget to UDC: %s\n", udc);
    }

    // 4. Handle soft_connect symlink and ownership
    char target[512], link_path[] = "/tmp/soft_connect";
    snprintf(target, sizeof(target), "/sys/class/udc/%s/soft_connect", udc);
    unlink(link_path);
    if (symlink(target, link_path) == 0) {
        change_owner(link_path, "registrar");
    }

    // 5. Wait for Endpoints and fix ownership
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