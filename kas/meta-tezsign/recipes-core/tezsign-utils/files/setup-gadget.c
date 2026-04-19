#define _XOPEN_SOURCE 500

#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/mount.h>
#include <fcntl.h>
#include <errno.h>
#include <pwd.h>
#include <grp.h>
#include <ftw.h>

#define GADGET_BASE "/sys/kernel/config/usb_gadget/g1"
#define FFS_DIR     "/dev/ffs/tezsign"
#define DATA_DIR    "/data/tezsign"

static uid_t target_uid;
static gid_t target_gid;

// Utility to write to ConfigFS attributes
int write_attr(const char *path, const char *fmt, ...) {
    char buf[256];
    va_list args;
    va_start(args, fmt);
    vsnprintf(buf, sizeof(buf), fmt, args);
    va_end(args);

    int fd = open(path, O_WRONLY | O_TRUNC);
    if (fd < 0) {
        fprintf(stderr, "Critical: Failed to open %s (%s)\n", path, strerror(errno));
        return -1;
    }
    if (write(fd, buf, strlen(buf)) < 0) {
        fprintf(stderr, "Critical: Failed writing to %s\n", path);
        close(fd);
        return -1;
    }
    close(fd);
    return 0;
}

// Recursive mkdir -p
int mkdir_p(const char *path) {
    char tmp[256];
    char *p = NULL;
    snprintf(tmp, sizeof(tmp), "%s", path);
    for (p = tmp + 1; *p; p++) {
        if (*p == '/') {
            *p = 0;
            mkdir(tmp, 0755);
            *p = '/';
        }
    }
    if (mkdir(tmp, 0755) != 0 && errno != EEXIST) return -1;
    return 0;
}

int chown_callback(const char *fpath, const struct stat *sb, int tflag, struct FTW *ftwbuf) {
    (void)sb;
    (void)tflag;
    (void)ftwbuf;

    if (lchown(fpath, target_uid, target_gid) != 0) {
        perror(fpath);
        return -1;
    }

    return 0;
}

int recursive_chown(const char *path, uid_t uid, gid_t gid) {
    target_uid = uid;
    target_gid = gid;

    return nftw(path, chown_callback, 20, FTW_PHYS);
}

int main() {
    char serial[64] = "000000000000"; // Default fallback
    FILE *f;

    // Try to load existing serial
    if (f = fopen("/app/tezsign_id", "r")) {
        if (fgets(serial, sizeof(serial), f)) {
            serial[strcspn(serial, "\n")] = 0;
        }
        fclose(f);
    } else {
        fprintf(stderr, "Warning: No serial file found. Using default.\n");
    }


    // Setup ConfigFS structure
    if (mkdir_p(GADGET_BASE "/strings/0x409") != 0) {
        fprintf(stderr, "Error: ConfigFS not mounted or write protected.\n");
        return EXIT_FAILURE;
    }

    if (write_attr(GADGET_BASE "/idVendor", "0x9997") != 0 ||
        write_attr(GADGET_BASE "/idProduct", "0x0001") != 0 ||
        write_attr(GADGET_BASE "/strings/0x409/serialnumber", "%s", serial) != 0 ||
        write_attr(GADGET_BASE "/strings/0x409/manufacturer", "TzC") != 0 ||
        write_attr(GADGET_BASE "/strings/0x409/product", "tezsign-gadget") != 0) {
        return EXIT_FAILURE;
    }

    // Create FFS Function and Link to Config
    mkdir_p(GADGET_BASE "/functions/ffs.tezsign");
    mkdir_p(GADGET_BASE "/configs/c.1/strings/0x409");
    
    if (symlink(GADGET_BASE "/functions/ffs.tezsign", GADGET_BASE "/configs/c.1/ffs.tezsign") != 0) {
        if (errno != EEXIST) {
            perror("Symlink to config failed");
            return EXIT_FAILURE;
        }
    }

    // Mount FunctionFS
    mkdir_p(FFS_DIR);
    if (mount("tezsign", FFS_DIR, "functionfs", 0, NULL) != 0) {
        if (errno != EBUSY) {
            perror("FunctionFS mount failed");
            return EXIT_FAILURE;
        }
    }

    uid_t reg_uid, tez_uid;
    gid_t dm_gid, reg_gid, tez_gid;

    struct passwd *pw;
    struct group *gr;

    // Get Registrar User & Group
    if (!(pw = getpwnam("registrar"))) { fprintf(stderr, "Missing user: registrar\n"); return 1; }
    reg_uid = pw->pw_uid;

    if (!(gr = getgrnam("registrar"))) { fprintf(stderr, "Missing group: registrar\n"); return 1; }
    reg_gid = gr->gr_gid;

    // Get Dev Manager Group
    if (!(gr = getgrnam("dev_manager"))) { fprintf(stderr, "Missing group: dev_manager\n"); return 1; }
    dm_gid = gr->gr_gid;

    // Get Tezsign User & Group
    if (!(pw = getpwnam("tezsign"))) { fprintf(stderr, "Missing user: tezsign\n"); return 1; }
    tez_uid = pw->pw_uid;

    if (!(gr = getgrnam("tezsign"))) { fprintf(stderr, "Missing group: tezsign\n"); return 1; }
    tez_gid = gr->gr_gid;

    // chmod 770 /dev/ffs/tezsign && chown :dev_manager
    chmod(FFS_DIR, 0770);
    if (chown(FFS_DIR, -1, dm_gid) != 0) {
        fprintf(stderr, "Failed to set group on %s (GID: %u): %s\n", 
                FFS_DIR, (unsigned int)dm_gid, strerror(errno));
        return EXIT_FAILURE;
    }

    // chown registrar:registrar /dev/ffs/tezsign/ep0
    if (chown(FFS_DIR "/ep0", reg_uid, reg_gid) != 0) {
        perror("Failed to set ownership on ep0");
        return EXIT_FAILURE;
    }

    // Setup /data/tezsign directory
    if (mkdir_p(DATA_DIR) != 0) {
        fprintf(stderr, "Error: Failed to create %s\n", DATA_DIR);
        return EXIT_FAILURE;
    }

    if (recursive_chown(DATA_DIR, tez_uid, tez_gid) != 0) {
        perror("Failed to set ownership on " DATA_DIR);
        return EXIT_FAILURE;
    }

    printf("USB Gadget success: %s\n", serial);
    return EXIT_SUCCESS;
}
