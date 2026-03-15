#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <ctype.h>
#include <unistd.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <sys/mount.h>

#define ID_FILE "/app/tezsign_id"
#define TMP_FILE "/tmp/tezsign_id.tmp"

void sanitize(char *src, char *dest) {
    int j = 0;
    for (int i = 0; src[i] != '\0' && j < 32; i++) {
        if (isalnum(src[i])) {
            dest[j++] = toupper(src[i]);
        }
    }
    dest[j] = '\0';
    // Pad to 12 if shorter
    while (strlen(dest) < 12) {
        strcat(dest, "0");
    }
}

int read_file(const char *path, char *buf, size_t size) {
    FILE *f = fopen(path, "r");
    if (!f) return 0;
    if (fgets(buf, size, f)) {
        fclose(f);
        return 1;
    }
    fclose(f);
    return 0;
}

int main() {
    if (access(ID_FILE, F_OK) == 0) return 0;

    char raw[64] = {0};
    char final_id[33] = {0};

    // Serial Source Hierarchy
    if (read_file("/etc/machine-id", raw, sizeof(raw))) ;
    else if (read_file("/sys/firmware/devicetree/base/serial-number", raw, sizeof(raw))) ;
    else if (read_file("/sys/block/mmcblk0/device/cid", raw, sizeof(raw))) ;
    else {
        // Fallback: Random
        int fd = open("/dev/urandom", O_RDONLY);
        unsigned char rand_buf[16];
        read(fd, rand_buf, 16);
        close(fd);
        for(int i=0; i<16; i++) sprintf(&raw[i*2], "%02x", rand_buf[i]);
    }

    sanitize(raw, final_id);

    // Persistence logic
    umask(0077);
    mount(NULL, "/app", NULL, MS_REMOUNT, NULL); // Remount RW
    
    FILE *f = fopen(ID_FILE, "w");
    if (f) {
        fputs(final_id, f);
        fclose(f);
    }

    mount(NULL, "/app", NULL, MS_REMOUNT | MS_RDONLY, NULL); // Remount RO
    printf("%s\n", final_id);

    return 0;
}