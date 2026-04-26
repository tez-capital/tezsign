#define _POSIX_C_SOURCE 200809L

#include <dirent.h>
#include <errno.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>
#include <time.h>

#define RETRY_INTERVAL_MS 100
#define WAIT_TIMEOUT_MS 5000

static void usage(const char *argv0) {
	fprintf(stderr, "usage: %s <fifo-priority-or-0> <irq-cpu|irq-cpulist|irq-mask> <token> [token ...]\n", argv0);
}

static bool is_numeric_name(const char *name) {
	const unsigned char *p = (const unsigned char *)name;

	if (*p == '\0') {
		return false;
	}

	for (; *p != '\0'; ++p) {
		if (*p < '0' || *p > '9') {
			return false;
		}
	}

	return true;
}

static bool parse_long(const char *text, long minimum, long maximum, long *value) {
	char *end = NULL;
	long parsed;

	errno = 0;
	parsed = strtol(text, &end, 10);
	if (errno != 0 || end == text || *end != '\0') {
		return false;
	}
	if (parsed < minimum || parsed > maximum) {
		return false;
	}

	*value = parsed;
	return true;
}

static bool read_comm(pid_t pid, char *buffer, size_t buffer_size) {
	char path[64];
	FILE *file;

	snprintf(path, sizeof(path), "/proc/%ld/comm", (long)pid);
	file = fopen(path, "r");
	if (file == NULL) {
		return false;
	}

	if (fgets(buffer, (int)buffer_size, file) == NULL) {
		fclose(file);
		return false;
	}
	fclose(file);

	buffer[strcspn(buffer, "\n")] = '\0';
	return true;
}

static bool matches_token(const char *text, char *const *tokens, int token_count) {
	int i;

	for (i = 0; i < token_count; ++i) {
		if (strstr(text, tokens[i]) != NULL) {
			return true;
		}
	}

	return false;
}

static bool parse_affinity_hex(const char *text, unsigned long long *mask_value) {
	char *end = NULL;

	errno = 0;
	*mask_value = strtoull(text, &end, 16);
	if (errno != 0 || end == text || *end != '\0' || *mask_value == 0) {
		return false;
	}

	return true;
}

static bool parse_affinity_cpulist(const char *text, unsigned long long *mask_value) {
	char buffer[128];
	char *item;
	char *saveptr = NULL;
	unsigned long long parsed_mask = 0;

	if (strlen(text) >= sizeof(buffer)) {
		return false;
	}

	memcpy(buffer, text, strlen(text) + 1);
	for (item = strtok_r(buffer, ",", &saveptr); item != NULL; item = strtok_r(NULL, ",", &saveptr)) {
		char *dash = strchr(item, '-');
		long start_cpu;
		long end_cpu;
		long cpu;

		if (*item == '\0') {
			return false;
		}
		if (dash == item || (dash != NULL && dash[1] == '\0')) {
			return false;
		}

		if (dash == NULL) {
			if (!parse_long(item, 0, 63, &start_cpu)) {
				return false;
			}
			parsed_mask |= 1ULL << start_cpu;
			continue;
		}

		*dash = '\0';
		if (!parse_long(item, 0, 63, &start_cpu) || !parse_long(dash + 1, 0, 63, &end_cpu) || end_cpu < start_cpu) {
			return false;
		}
		for (cpu = start_cpu; cpu <= end_cpu; ++cpu) {
			parsed_mask |= 1ULL << cpu;
		}
	}

	if (parsed_mask == 0) {
		return false;
	}

	*mask_value = parsed_mask;
	return true;
}

static bool parse_affinity_spec(const char *text, unsigned long long *mask_value, char *mask_text, size_t mask_text_size) {
	long cpu_index;

	if (strchr(text, ',') != NULL || strchr(text, '-') != NULL) {
		if (!parse_affinity_cpulist(text, mask_value)) {
			return false;
		}
	} else if ((text[0] == '0' && (text[1] == 'x' || text[1] == 'X')) || strpbrk(text, "abcdefABCDEF") != NULL) {
		if (!parse_affinity_hex(text, mask_value)) {
			return false;
		}
	} else {
		if (!parse_long(text, 0, 63, &cpu_index)) {
			return false;
		}
		*mask_value = 1ULL << cpu_index;
	}

	snprintf(mask_text, mask_text_size, "%llx", (unsigned long long)*mask_value);
	return true;
}

static bool read_hex_mask(const char *path, unsigned long long *value) {
	char buffer[128];
	char compact[128];
	FILE *file;
	int write_index = 0;
	int read_index;
	char *end = NULL;

	file = fopen(path, "r");
	if (file == NULL) {
		return false;
	}
	if (fgets(buffer, sizeof(buffer), file) == NULL) {
		fclose(file);
		return false;
	}
	fclose(file);

	for (read_index = 0; buffer[read_index] != '\0' && write_index < (int)(sizeof(compact) - 1); ++read_index) {
		char ch = buffer[read_index];

		if (ch == ',' || ch == '\n') {
			continue;
		}
		compact[write_index++] = ch;
	}
	compact[write_index] = '\0';
	if (compact[0] == '\0') {
		return false;
	}

	errno = 0;
	*value = strtoull(compact, &end, 16);
	if (errno != 0 || end == compact || *end != '\0') {
		return false;
	}

	return true;
}

static int set_irq_affinity(const char *irq, const char *affinity_spec, unsigned long long mask_value, const char *mask_text, bool *changed) {
	char path[64];
	unsigned long long current_mask = 0;
	FILE *file;

	*changed = false;
	snprintf(path, sizeof(path), "/proc/irq/%s/smp_affinity", irq);
	if (read_hex_mask(path, &current_mask) && current_mask == mask_value) {
		return 0;
	}

	file = fopen(path, "w");
	if (file == NULL) {
		if (errno == ENOENT) {
			return 0;
		}
		fprintf(stderr, "tune-interrupts: failed to open %s: %s\n", path, strerror(errno));
		return -1;
	}
	if (fprintf(file, "%s", mask_text) < 0) {
		fprintf(stderr, "tune-interrupts: failed to write %s: %s\n", path, strerror(errno));
		fclose(file);
		return -1;
	}
	if (fclose(file) != 0) {
		fprintf(stderr, "tune-interrupts: failed to close %s: %s\n", path, strerror(errno));
		return -1;
	}

	*changed = true;
	printf("tune-interrupts: pinned irq %s to %s\n", irq, affinity_spec);
	fflush(stdout);
	return 0;
}

static int scan_irqs(const char *affinity_spec, char *const *tokens, int token_count, unsigned long long mask_value, const char *mask_text, int *matches, int *changes) {
	FILE *file;
	char line[512];
	int local_matches = 0;
	int local_changes = 0;

	file = fopen("/proc/interrupts", "r");
	if (file == NULL) {
		fprintf(stderr, "tune-interrupts: failed to open /proc/interrupts: %s\n", strerror(errno));
		return -1;
	}

	while (fgets(line, sizeof(line), file) != NULL) {
		char irq[32];
		char *colon;
		char *start;
		size_t length;
		bool changed;

		colon = strchr(line, ':');
		if (colon == NULL || !matches_token(line, tokens, token_count)) {
			continue;
		}

		start = line;
		while (*start == ' ' || *start == '\t') {
			start++;
		}
		length = (size_t)(colon - start);
		if (length == 0 || length >= sizeof(irq)) {
			continue;
		}
		memcpy(irq, start, length);
		irq[length] = '\0';
		if (!is_numeric_name(irq)) {
			continue;
		}

		local_matches++;
		if (set_irq_affinity(irq, affinity_spec, mask_value, mask_text, &changed) != 0) {
			fclose(file);
			return -1;
		}
		if (changed) {
			local_changes++;
		}
	}

	fclose(file);
	*matches = local_matches;
	*changes = local_changes;
	return 0;
}

static int set_fifo_priority(pid_t pid, const char *comm, int priority, bool *changed) {
	struct sched_param param;
	int policy;

	*changed = false;
	if (priority <= 0) {
		return 0;
	}
	errno = 0;
	policy = sched_getscheduler(pid);
	if (policy < 0) {
		if (errno == ESRCH) {
			return 0;
		}
		fprintf(stderr, "tune-interrupts: sched_getscheduler failed for %s (%ld): %s\n",
			comm, (long)pid, strerror(errno));
		return -1;
	}

	if (sched_getparam(pid, &param) != 0) {
		if (errno == ESRCH) {
			return 0;
		}
		fprintf(stderr, "tune-interrupts: sched_getparam failed for %s (%ld): %s\n",
			comm, (long)pid, strerror(errno));
		return -1;
	}

	if (policy == SCHED_FIFO && param.sched_priority >= priority) {
		return 0;
	}

	memset(&param, 0, sizeof(param));
	param.sched_priority = priority;
	if (sched_setscheduler(pid, SCHED_FIFO, &param) != 0) {
		if (errno == ESRCH) {
			return 0;
		}
		fprintf(stderr, "tune-interrupts: sched_setscheduler failed for %s (%ld): %s\n",
			comm, (long)pid, strerror(errno));
		return -1;
	}

	*changed = true;
	printf("tune-interrupts: raised %s (%ld) to fifo/%d\n", comm, (long)pid, priority);
	fflush(stdout);
	return 0;
}

static int scan_threads(int priority, char *const *tokens, int token_count, int *matches, int *changes) {
	DIR *proc_dir;
	struct dirent *entry;
	int local_matches = 0;
	int local_changes = 0;

	proc_dir = opendir("/proc");
	if (proc_dir == NULL) {
		fprintf(stderr, "tune-interrupts: failed to open /proc: %s\n", strerror(errno));
		return -1;
	}

	while ((entry = readdir(proc_dir)) != NULL) {
		pid_t pid;
		char comm[256];
		bool changed;

		if (!is_numeric_name(entry->d_name)) {
			continue;
		}

		pid = (pid_t)strtol(entry->d_name, NULL, 10);
		if (!read_comm(pid, comm, sizeof(comm))) {
			continue;
		}
		if (strncmp(comm, "irq/", 4) != 0 || !matches_token(comm, tokens, token_count)) {
			continue;
		}

		local_matches++;
		if (set_fifo_priority(pid, comm, priority, &changed) != 0) {
			closedir(proc_dir);
			return -1;
		}
		if (changed) {
			local_changes++;
		}
	}

	closedir(proc_dir);
	*matches = local_matches;
	*changes = local_changes;
	return 0;
}

int main(int argc, char **argv) {
	long priority;
	bool tune_threads;
	int elapsed_ms = 0;
	int max_irq_matches = 0;
	int total_irq_changes = 0;
	int max_thread_matches = 0;
	int total_thread_changes = 0;
	unsigned long long mask_value;
	char mask_text[32];
	struct timespec delay;

	if (argc < 4) {
		usage(argv[0]);
		return EXIT_FAILURE;
	}
	if (!parse_long(argv[1], 0, 99, &priority)) {
		usage(argv[0]);
		return EXIT_FAILURE;
	}
	if (!parse_affinity_spec(argv[2], &mask_value, mask_text, sizeof(mask_text))) {
		usage(argv[0]);
		return EXIT_FAILURE;
	}
	tune_threads = priority > 0;

	delay.tv_sec = 0;
	delay.tv_nsec = RETRY_INTERVAL_MS * 1000000L;

	while (true) {
		int irq_matches = 0;
		int irq_changes = 0;
		int thread_matches = 0;
		int thread_changes = 0;

		if (scan_irqs(argv[2], &argv[3], argc - 3, mask_value, mask_text, &irq_matches, &irq_changes) != 0) {
			return EXIT_FAILURE;
		}
		if (tune_threads) {
			if (scan_threads((int)priority, &argv[3], argc - 3, &thread_matches, &thread_changes) != 0) {
				return EXIT_FAILURE;
			}
		}

		if (irq_matches > max_irq_matches) {
			max_irq_matches = irq_matches;
		}
		total_irq_changes += irq_changes;
		if (thread_matches > max_thread_matches) {
			max_thread_matches = thread_matches;
		}
		total_thread_changes += thread_changes;

		if (max_irq_matches > 0 && (!tune_threads || max_thread_matches > 0)) {
			if (tune_threads) {
				printf("tune-interrupts: processed %d matching irqs (%d changed) and %d matching irq threads (%d changed)\n",
					max_irq_matches, total_irq_changes, max_thread_matches, total_thread_changes);
			} else {
				printf("tune-interrupts: processed %d matching irqs (%d changed); irq thread tuning disabled\n",
					max_irq_matches, total_irq_changes);
			}
			fflush(stdout);
			return EXIT_SUCCESS;
		}
		if (elapsed_ms >= WAIT_TIMEOUT_MS) {
			if (max_irq_matches > 0 || max_thread_matches > 0) {
				if (tune_threads) {
					printf("tune-interrupts: processed %d matching irqs (%d changed) and %d matching irq threads (%d changed) before timeout\n",
						max_irq_matches, total_irq_changes, max_thread_matches, total_thread_changes);
				} else {
					printf("tune-interrupts: processed %d matching irqs (%d changed) before timeout; irq thread tuning disabled\n",
						max_irq_matches, total_irq_changes);
				}
			} else {
				printf("tune-interrupts: no matching irqs or irq threads found\n");
			}
			fflush(stdout);
			return EXIT_SUCCESS;
		}

		nanosleep(&delay, NULL);
		elapsed_ms += RETRY_INTERVAL_MS;
	}
}