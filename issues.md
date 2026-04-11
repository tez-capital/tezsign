# RPi Zero 2 W Yocto Performance Issues

## Scope

This report is limited to the Raspberry Pi Zero 2 W signing regression relative to the Armbian image you actually benchmarked.

The hot path is `KeyRing.SignAndUpdate`, which:

- signs a short BLS payload,
- persists watermark state synchronously before returning,
- depends on SD-card write latency, CPU ramp behavior, and kernel driver behavior.

## Corrections to the comparison baseline

The following are **not** useful Yocto-vs-Armbian differentiators for the benchmark as you described it:

- The gadget binary is the same in both flows. `tools/builder/assets/tezsign` and `kas/meta-tezsign/recipes-core/images/files/tezsign` are byte-identical.
- `schedutil` plus a `600000` minimum CPU frequency is shared.
- `data=journal` on `/data` is shared.

There is also an important source-of-truth issue:

- The repo CI workflow at `.github/workflows/release.yml` still pins Raspberry Pi Armbian jobs to `armbian_kernel_branch: "legacy"`.
- You clarified that the Armbian image used for the benchmark was **mainline**, not the legacy branch from that workflow.

That means the workflow file cannot be used as evidence for the exact kernel branch of the benchmarked Armbian image.

## High-confidence differentials

### 1. Yocto enables RT `SCHED_FIFO` for `tezsign` and `ffs_registrar`, while the Armbian image does not

Evidence:

- `kas/meta-tezsign/recipes-core/tezsign-core/files/tezsign.service` sets:
  - `CPUSchedulingPolicy=fifo`
  - `CPUSchedulingPriority=50`
- `kas/meta-tezsign/recipes-core/tezsign-core/files/ffs_registrar.service` sets the same RT policy.
- The Armbian-side units in `tools/builder/assets/tezsign.service` and `tools/builder/assets/ffs_registrar.service` do not set RT scheduling.

Why this matters:

- `SignAndUpdate` is not CPU-only work. It waits on a durable state write and interacts with the gadget stack.
- `SCHED_FIFO` can reduce fairness for the kernel and userspace work that the request still depends on.
- On a small Cortex-A53 system, that can hurt total end-to-end throughput even when it reduces jitter for one thread.

Impact:

- High.
- This is the clearest verified runtime differential.

### 2. Yocto forces the MMC scheduler to `none`; no equivalent setting exists in the Armbian image flow in this repo

Evidence:

- `kas/meta-tezsign/recipes-core/tezsign-core/files/io-scheduler.conf` writes `/sys/block/mmcblk0/queue/scheduler = none`.
- `kas/meta-tezsign/recipes-core/tezsign-core/files/99-io-performance.rules` enforces the same for `mmcblk*` devices.
- There is no corresponding `queue/scheduler` tuning in `tools/builder` or `armbian_userpatches`.

Why this matters:

- Your hot path performs tiny synchronous updates to the state file.
- For SD cards, `none` is not universally better than the kernel default or `mq-deadline` for this pattern.
- If the Armbian image leaves the scheduler alone, this becomes a real storage-path differential.

Impact:

- Medium.
- Card-dependent, but credible.

### 3. The `/data` mount mode is shared, but the `/data` ext4 formatting is not

Evidence:

- The Armbian image builder formats the data partition in `tools/builder/partition.go` with:
  - `-J size=8`
  - `-m 0`
  - `-I 1024`
  - `-O inline_data,fast_commit`
- The Yocto image creates `/data` from `kas/meta-tezsign/recipes-core/images/files/storage.wks.in` with only:
  - `--mkfs-extraopts="-O has_journal"`
  - `--fsoptions="rw,noatime,nofail,data=journal"`

Why this matters:

- The shared `data=journal` mount option means mount mode alone is not the difference.
- But the Armbian-derived image uses a more aggressively tuned ext4 layout for a tiny appliance data partition.
- `fast_commit` is a plausible advantage for repeated small sync-heavy updates.

Impact:

- Medium.
- This is a more plausible filesystem explanation than `data=journal` by itself.

## Kernel status: unresolved, not proven by the current repo evidence

### 4. Kernel differences may still matter, but the old `legacy` claim should not be used

Evidence:

- Yocto builds its Raspberry Pi kernel from `kas/meta-tezsign/recipes-kernel/linux-mainline/linux-mainline_6.18.bb` using:
  - `SRC_URI = "git://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git;branch=master;protocol=https"`
  - `SRCREV = "0ff41df1cb268fc69e703a08a57ee14ae967d0ca"`
- The repo CI workflow still says `legacy` for Raspberry Pi Armbian jobs, but you stated the benchmarked Armbian image used **mainline**.

Why this matters:

- Kernel differences are still a credible class of cause because this workload combines DWC2 gadget traffic with synchronous SD writes.
- But I can no longer honestly claim `legacy` versus upstream mainline from the repo as the explanation for your benchmark.
- At most, the current evidence supports a weaker statement: Yocto uses a specific upstream `torvalds/master` snapshot, while the exact Armbian mainline kernel used in the benchmark is not captured in this repo.

Impact:

- Unknown.
- Still worth investigating, but not a high-confidence item in this report.

## Important setup issue, but not the direct cause

### 5. The RT-style Yocto kernel fragment is not landing in the final built kernel

Evidence:

- `kas/meta-tezsign/recipes-kernel/linux-mainline/linux-mainline-6.18/tezsign-common.cfg` asks for `PREEMPT_RT=y`, `HZ_1000=y`, `NO_HZ_FULL=y`, and `CPU_IDLE=n`.
- The final built Zero 2 W kernel config at `kas/build/tmp-musl/work/raspberrypi0_2w_tezsign-oe-linux-musl/linux-mainline/6.18/build/.config` still shows:
  - `CONFIG_PREEMPT=y`
  - `CONFIG_HZ_250=y`
  - `CONFIG_NO_HZ_IDLE=y`
  - `CONFIG_CPU_IDLE=y`

Why this matters:

- This does not explain the current regression, because those requested RT settings are not actually active.
- But it is still a configuration-quality problem: the kernel you are building is not the kernel your fragments suggest you intended to build.

Impact:

- Low for the current regression.
- High for debugging clarity and reproducibility.

## Suggested ranking for next benchmarks

1. Remove `CPUSchedulingPolicy=fifo` and `CPUSchedulingPriority` from `tezsign.service` and `ffs_registrar.service`.
2. Stop forcing `mmcblk0` to `none` and compare against the kernel default.
3. Reformat Yocto `/data` to mirror the Armbian builder's ext4 options, especially `fast_commit`.
4. Only after that, compare the exact running kernel versions and configs from the benchmarked Armbian mainline image versus the Yocto image.

## Bottom line

After removing the shared settings and the invalid `legacy` assumption, the strongest verified Yocto-specific suspects are:

1. RT `SCHED_FIFO` services,
2. forced MMC scheduler `none`,
3. a less tuned ext4 `/data` format.

Kernel provenance remains a plausible factor, but it is not pinned down well enough in the current repo evidence to rank as a confirmed issue.