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
#include <ftw.h>
#include <grp.h>
#include <pwd.h>

#define GADGET_BASE "/sys/kernel/config/usb_gadget/g1"
#define FFS_DIR     "/dev/ffs/tezsign"
#define DATA_DIR    "/data/tezsign"

static uid_t target_uid;
static gid_t target_gid;

static void log_message(const char *level, const char *fmt, ...) {
    FILE *stream = stdout;
    va_list args;

    if (strcmp(level, "WARN") == 0 || strcmp(level, "ERROR") == 0) {
        stream = stderr;
    }

    fprintf(stream, "setup-gadget: %s: ", level);
    va_start(args, fmt);
    vfprintf(stream, fmt, args);
    va_end(args);
    fputc('\n', stream);
    fflush(stream);
}

static void log_errno_message(const char *context, const char *path) {
    int saved_errno = errno;

    if (path != NULL) {
        log_message("ERROR", "%s %s (%s)", context, path, strerror(saved_errno));
    } else {
        log_message("ERROR", "%s (%s)", context, strerror(saved_errno));
    }
}

// Utility to write to ConfigFS attributes
static int write_attr(const char *path, const char *fmt, ...) {
    char buf[256];
    int fd;
    va_list args;

    va_start(args, fmt);
    vsnprintf(buf, sizeof(buf), fmt, args);
    va_end(args);

    fd = open(path, O_WRONLY | O_TRUNC);
    if (fd < 0) {
        log_errno_message("Critical: failed to open", path);
        return -1;
    }
    if (write(fd, buf, strlen(buf)) < 0) {
        log_errno_message("Critical: failed writing to", path);
        close(fd);
        return -1;
    }
    close(fd);
    return 0;
}

// Recursive mkdir -p
static int mkdir_p(const char *path) {
    char tmp[256];
    char *p;

    snprintf(tmp, sizeof(tmp), "%s", path);
    for (p = tmp + 1; *p; p++) {
        if (*p == '/') {
            *p = 0;
            if (mkdir(tmp, 0755) != 0 && errno != EEXIST) {
                log_errno_message("Failed to create", tmp);
                return -1;
            }
            *p = '/';
        }
    }
    if (mkdir(tmp, 0755) != 0 && errno != EEXIST) {
        log_errno_message("Failed to create", tmp);
        return -1;
    }
    return 0;
}

static int sync_path(const char *path) {
    int fd = open(path, O_RDONLY);
    if (fd < 0) {
        log_errno_message("Failed to open for sync", path);
        return -1;
    }
    int ret = fsync(fd);
    if (ret != 0) log_errno_message("Failed to sync", path);
    close(fd);
    return ret;
}

static int ensure_mode(const char *path, mode_t want_mode) {
    struct stat st;
    mode_t current_mode;

    if (stat(path, &st) != 0) {
        log_errno_message("Failed to stat", path);
        return -1;
    }

    current_mode = st.st_mode & 07777;
    if (current_mode == want_mode) {
        return 0;
    }

    if (chmod(path, want_mode) != 0) {
        log_errno_message("Failed to change mode on", path);
        return -1;
    }

    return 0;
}

static int ensure_owner(const char *path, uid_t uid, gid_t gid) {
    struct stat st;

    if (stat(path, &st) != 0) {
        log_errno_message("Failed to stat", path);
        return -1;
    }

    if (st.st_uid == uid && st.st_gid == gid) {
        return 0;
    }

    if (chown(path, uid, gid) != 0) {
        log_errno_message("Failed to set ownership on", path);
        return -1;
    }

    return 0;
}

static int ensure_group(const char *path, gid_t gid) {
    struct stat st;

    if (stat(path, &st) != 0) {
        log_errno_message("Failed to stat", path);
        return -1;
    }

    if (st.st_gid == gid) {
        return 0;
    }

    if (chown(path, (uid_t)-1, gid) != 0) {
        log_errno_message("Failed to set group on", path);
        return -1;
    }

    return 0;
}

static int chown_callback(const char *fpath, const struct stat *sb, int tflag, struct FTW *ftwbuf) {
    (void)tflag;
    (void)ftwbuf;

    if (sb->st_uid == target_uid && sb->st_gid == target_gid) {
        return 0;
    }

    if (lchown(fpath, target_uid, target_gid) != 0) {
        log_errno_message("Failed to change ownership on", fpath);
        return -1;
    }

    return 0;
}

static int recursive_chown(const char *path, uid_t uid, gid_t gid) {
    target_uid = uid;
    target_gid = gid;

    return nftw(path, chown_callback, 64, FTW_PHYS);
}

int main() {
    char serial[64] = "000000000000"; // Default fallback
    FILE *f;
    uid_t reg_uid, tez_uid;
    gid_t dm_gid, reg_gid, tez_gid;
    struct passwd *pw;
    struct group *gr;

    // Try to load existing serial
    if ((f = fopen("/app/tezsign_id", "r")) != NULL) {
        if (fgets(serial, sizeof(serial), f)) {
            serial[strcspn(serial, "\n")] = 0;
        }
        fclose(f);
        log_message("INFO", "Using serial %s", serial);
    } else {
        log_message("WARN", "No serial file found, using default serial %s", serial);
    }

    // Resolve users and groups before touching the data directory.
    log_message("INFO", "Resolving required users and groups");
    if (!(pw = getpwnam("registrar"))) { log_message("ERROR", "Missing user: registrar"); return 1; }
    reg_uid = pw->pw_uid;

    if (!(gr = getgrnam("registrar"))) { log_message("ERROR", "Missing group: registrar"); return 1; }
    reg_gid = gr->gr_gid;

    if (!(gr = getgrnam("dev_manager"))) { log_message("ERROR", "Missing group: dev_manager"); return 1; }
    dm_gid = gr->gr_gid;

    if (!(pw = getpwnam("tezsign"))) { log_message("ERROR", "Missing user: tezsign"); return 1; }
    tez_uid = pw->pw_uid;

    if (!(gr = getgrnam("tezsign"))) { log_message("ERROR", "Missing group: tezsign"); return 1; }
    tez_gid = gr->gr_gid;
    log_message("INFO", "Resolved registrar uid=%u gid=%u, dev_manager gid=%u, tezsign uid=%u gid=%u",
            (unsigned int)reg_uid, (unsigned int)reg_gid, (unsigned int)dm_gid,
            (unsigned int)tez_uid, (unsigned int)tez_gid);

    // Prepare persistent storage ownership before exposing the gadget.
    log_message("INFO", "Ensuring data directory %s", DATA_DIR);
    if (mkdir_p(DATA_DIR) != 0) {
        log_message("ERROR", "Failed to create %s", DATA_DIR);
        return EXIT_FAILURE;
    }

    log_message("INFO", "Recursively applying ownership under %s", DATA_DIR);
    if (recursive_chown(DATA_DIR, tez_uid, tez_gid) != 0) {
        log_message("ERROR", "Failed to update ownership under %s", DATA_DIR);
        return EXIT_FAILURE;
    }

    // Setup ConfigFS structure
    log_message("INFO", "Ensuring ConfigFS structure under %s", GADGET_BASE);
    if (mkdir_p(GADGET_BASE "/strings/0x409") != 0) {
        log_message("ERROR", "ConfigFS not mounted or write protected");
        return EXIT_FAILURE;
    }

    log_message("INFO", "Writing gadget descriptors");
    if (write_attr(GADGET_BASE "/idVendor", "0x9997") != 0 ||
        write_attr(GADGET_BASE "/idProduct", "0x0001") != 0 ||
        write_attr(GADGET_BASE "/strings/0x409/serialnumber", "%s", serial) != 0 ||
        write_attr(GADGET_BASE "/strings/0x409/manufacturer", "TzC") != 0 ||
        write_attr(GADGET_BASE "/strings/0x409/product", "tezsign-gadget") != 0) {
        return EXIT_FAILURE;
    }

    // Create FFS Function and Link to Config
    log_message("INFO", "Preparing FunctionFS configuration");
    if (mkdir_p(GADGET_BASE "/functions/ffs.tezsign") != 0) {
        return EXIT_FAILURE;
    }
    if (mkdir_p(GADGET_BASE "/configs/c.1/strings/0x409") != 0) {
        return EXIT_FAILURE;
    }
    
    if (symlink(GADGET_BASE "/functions/ffs.tezsign", GADGET_BASE "/configs/c.1/ffs.tezsign") != 0) {
        if (errno != EEXIST) {
            log_errno_message("Symlink to config failed at", GADGET_BASE "/configs/c.1/ffs.tezsign");
            return EXIT_FAILURE;
        }
        log_message("INFO", "FunctionFS config symlink already exists");
    }

    // Mount FunctionFS
    log_message("INFO", "Ensuring FunctionFS mountpoint %s", FFS_DIR);
    if (mkdir_p(FFS_DIR) != 0) {
        return EXIT_FAILURE;
    }
    log_message("INFO", "Mounting FunctionFS at %s", FFS_DIR);
    if (mount("tezsign", FFS_DIR, "functionfs", 0, NULL) != 0) {
        if (errno != EBUSY) {
            log_errno_message("FunctionFS mount failed at", FFS_DIR);
            return EXIT_FAILURE;
        }
        log_message("INFO", "FunctionFS already mounted at %s", FFS_DIR);
    }

    // chmod 770 /dev/ffs/tezsign && chown :dev_manager
    log_message("INFO", "Applying permissions to %s", FFS_DIR);
    if (ensure_mode(FFS_DIR, 0770) != 0) {
        return EXIT_FAILURE;
    }
    if (ensure_group(FFS_DIR, dm_gid) != 0) {
        return EXIT_FAILURE;
    }
    if (sync_path(FFS_DIR) != 0) {
        return EXIT_FAILURE;
    }

    // chown registrar:registrar /dev/ffs/tezsign/ep0
    log_message("INFO", "Applying ownership to %s", FFS_DIR "/ep0");
    if (ensure_owner(FFS_DIR "/ep0", reg_uid, reg_gid) != 0) {
        return EXIT_FAILURE;
    }

    log_message("INFO", "USB Gadget success: %s", serial);
    return EXIT_SUCCESS;
}