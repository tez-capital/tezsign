#define _XOPEN_SOURCE 500

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <errno.h>
#include <pwd.h>
#include <grp.h>
#include <ftw.h>

#define FFS_DIR  "/dev/ffs/tezsign"
#define DATA_DIR "/data/tezsign"

static uid_t target_uid;
static gid_t target_gid;

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

    if (mkdir(tmp, 0755) != 0 && errno != EEXIST) {
        return -1;
    }

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
    uid_t reg_uid, tez_uid;
    gid_t dm_gid, reg_gid, tez_gid;

    struct passwd *pw;
    struct group *gr;

    if (!(pw = getpwnam("registrar"))) { fprintf(stderr, "Missing user: registrar\n"); return EXIT_FAILURE; }
    reg_uid = pw->pw_uid;

    if (!(gr = getgrnam("registrar"))) { fprintf(stderr, "Missing group: registrar\n"); return EXIT_FAILURE; }
    reg_gid = gr->gr_gid;

    if (!(gr = getgrnam("dev_manager"))) { fprintf(stderr, "Missing group: dev_manager\n"); return EXIT_FAILURE; }
    dm_gid = gr->gr_gid;

    if (!(pw = getpwnam("tezsign"))) { fprintf(stderr, "Missing user: tezsign\n"); return EXIT_FAILURE; }
    tez_uid = pw->pw_uid;

    if (!(gr = getgrnam("tezsign"))) { fprintf(stderr, "Missing group: tezsign\n"); return EXIT_FAILURE; }
    tez_gid = gr->gr_gid;

    if (chmod(FFS_DIR, 0770) != 0) {
        perror("Failed to chmod " FFS_DIR);
        return EXIT_FAILURE;
    }

    if (chown(FFS_DIR, -1, dm_gid) != 0) {
        fprintf(stderr, "Failed to set group on %s (GID: %u): %s\n",
                FFS_DIR, (unsigned int)dm_gid, strerror(errno));
        return EXIT_FAILURE;
    }

    if (chown(FFS_DIR "/ep0", reg_uid, reg_gid) != 0) {
        perror("Failed to set ownership on " FFS_DIR "/ep0");
        return EXIT_FAILURE;
    }

    if (mkdir_p(DATA_DIR) != 0) {
        fprintf(stderr, "Error: Failed to create %s\n", DATA_DIR);
        return EXIT_FAILURE;
    }

    if (recursive_chown(DATA_DIR, tez_uid, tez_gid) != 0) {
        perror("Failed to set ownership on " DATA_DIR);
        return EXIT_FAILURE;
    }

    printf("Filesystem permissions ready.\n");
    return EXIT_SUCCESS;
}
