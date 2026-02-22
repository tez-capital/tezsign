package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	diskpartition "github.com/diskfs/go-diskfs/partition"
	"github.com/diskfs/go-diskfs/partition/gpt"
	"github.com/diskfs/go-diskfs/partition/mbr"
	"github.com/diskfs/go-diskfs/partition/part"
	"github.com/samber/lo"
	"github.com/tez-capital/tezsign/tools/common"
	"github.com/tez-capital/tezsign/tools/constants"
)

type partition struct {
	start       uint64
	end         uint64
	sectorCount uint64
}

type partitions struct {
	size uint64
	root partition
	app  partition
	data partition
}

func selectLargestPartition(candidates []part.Partition) part.Partition {
	var (
		best     part.Partition
		bestSize int64
	)
	for _, p := range candidates {
		if p == nil {
			continue
		}
		size := p.GetSize()
		if size <= 0 {
			continue
		}
		if best == nil || size > bestSize {
			best = p
			bestSize = size
		}
	}
	return best
}

func isLikelyLinuxRootGPTType(t gpt.Type) bool {
	switch t {
	case gpt.LinuxFilesystem, gpt.LinuxServerData, gpt.LinuxRootArm64, gpt.LinuxRootArm, gpt.LinuxRootX86_64, gpt.LinuxRootX86, gpt.LinuxRootIA64:
		return true
	default:
		return false
	}
}

func selectRootPartition(table diskpartition.Table, logger *slog.Logger) (part.Partition, error) {
	switch typed := table.(type) {
	case *gpt.Table:
		if len(typed.Partitions) == 0 {
			return nil, errors.New("GPT image has no partitions")
		}

		// Prefer explicit root labels if present.
		for i, p := range typed.Partitions {
			if p == nil {
				continue
			}
			name := strings.ToLower(strings.TrimSpace(p.Name))
			if name == "root" || name == "rootfs" {
				logger.Info("Selected rootfs partition by GPT label", slog.Int("index", i), slog.String("name", p.Name), slog.String("type", string(p.Type)))
				return p, nil
			}
		}

		// Next, prefer Linux-like root partition types and pick the largest.
		linuxCandidates := make([]part.Partition, 0)
		for _, p := range typed.Partitions {
			if p == nil {
				continue
			}
			if isLikelyLinuxRootGPTType(p.Type) {
				linuxCandidates = append(linuxCandidates, p)
			}
		}
		if root := selectLargestPartition(linuxCandidates); root != nil {
			logger.Info("Selected rootfs partition as largest GPT Linux candidate")
			return root, nil
		}

		// Fallback: largest defined GPT partition.
		all := make([]part.Partition, 0, len(typed.Partitions))
		for _, p := range typed.Partitions {
			all = append(all, p)
		}
		if root := selectLargestPartition(all); root != nil {
			logger.Warn("Falling back to largest GPT partition as rootfs candidate")
			return root, nil
		}
		return nil, errors.New("unable to select GPT root partition")
	case *mbr.Table:
		if len(typed.Partitions) == 0 {
			return nil, errors.New("MBR image has no partitions")
		}

		linuxCandidates := make([]part.Partition, 0)
		nonZero := make([]part.Partition, 0)
		for _, p := range typed.Partitions {
			if p == nil || p.Size == 0 {
				continue
			}
			nonZero = append(nonZero, p)
			if p.Type == mbr.Linux {
				linuxCandidates = append(linuxCandidates, p)
			}
		}

		if root := selectLargestPartition(linuxCandidates); root != nil {
			logger.Info("Selected rootfs partition as largest MBR Linux candidate")
			return root, nil
		}
		if root := selectLargestPartition(nonZero); root != nil {
			logger.Warn("Falling back to largest non-empty MBR partition as rootfs candidate")
			return root, nil
		}
		return nil, errors.New("unable to select MBR root partition")
	default:
		parts := table.GetPartitions()
		if root := selectLargestPartition(parts); root != nil {
			logger.Warn("Selected rootfs partition from generic table fallback (largest partition)")
			return root, nil
		}
		return nil, errors.New("unable to select root partition for partition table type")
	}
}

func resolveLogicalBlockSize(table diskpartition.Table, fallback int64) int64 {
	switch typed := table.(type) {
	case *gpt.Table:
		if typed.LogicalSectorSize > 0 {
			return int64(typed.LogicalSectorSize)
		}
	case *mbr.Table:
		if typed.LogicalSectorSize > 0 {
			return int64(typed.LogicalSectorSize)
		}
	}

	if fallback > 0 {
		return fallback
	}
	return 0
}

func openImageWithDetectedSectorSize(imagePath string, logger *slog.Logger) (*disk.Disk, diskpartition.Table, int64, error) {
	type probe struct {
		name       string
		openOption diskfs.OpenOpt
	}
	probes := []probe{
		{name: "default"}, // diskfs default (typically 512 for regular image files)
		{name: "512", openOption: diskfs.WithSectorSize(diskfs.SectorSize512)},
		{name: "1024", openOption: diskfs.WithSectorSize(diskfs.SectorSize(1024))},
		{name: "2048", openOption: diskfs.WithSectorSize(diskfs.SectorSize(2048))},
		{name: "4096", openOption: diskfs.WithSectorSize(diskfs.SectorSize4k)},
	}

	var firstErr error
	for _, probe := range probes {
		var (
			img *disk.Disk
			err error
		)
		if probe.openOption == nil {
			img, err = diskfs.Open(imagePath)
		} else {
			img, err = diskfs.Open(imagePath, probe.openOption)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		table, err := img.GetPartitionTable()
		if err != nil {
			_ = img.Close()
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		logicalBlockSize := resolveLogicalBlockSize(table, img.LogicalBlocksize)
		if logicalBlockSize <= 0 {
			_ = img.Close()
			if firstErr == nil {
				firstErr = errors.New("could not determine logical block size from image")
			}
			continue
		}

		if probe.name != "default" {
			logger.Info("Detected image sector size using probe", slog.String("probe", probe.name), slog.Int64("logical_block_size", logicalBlockSize))
		}
		return img, table, logicalBlockSize, nil
	}

	if firstErr == nil {
		firstErr = errors.New("failed to open image and detect logical block size")
	}
	return nil, nil, 0, firstErr
}

func parseResize2fsMinBlocks(output string) (uint64, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "Estimated minimum size of the filesystem:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			break
		}

		blocks, err := strconv.ParseUint(fields[len(fields)-1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse minimum filesystem blocks from %q: %w", line, err)
		}
		return blocks, nil
	}

	if err := scanner.Err(); err != nil {
		return 0, err
	}

	return 0, errors.New("resize2fs minimum size line not found")
}

func canRunRootfsResizeTools() bool {
	if _, err := exec.LookPath("e2fsck"); err != nil {
		return false
	}
	if _, err := exec.LookPath("resize2fs"); err != nil {
		return false
	}
	if _, err := exec.LookPath("dumpe2fs"); err != nil {
		return false
	}
	return true
}

func normalizeImageID(imageID string) string {
	base := strings.ToLower(strings.TrimSpace(imageID))
	return strings.TrimSuffix(base, ".dev")
}

type rootfsSizingConfig struct {
	enforceFixedSize bool
	fixedSizeMB      uint64
}

func rootfsSizingConfigForImage(imageID string) (rootfsSizingConfig, error) {
	switch normalizeImageID(imageID) {
	case "raspberry_pi":
		return rootfsSizingConfig{
			enforceFixedSize: true,
			fixedSizeMB:      rootfsPartitionSizeRaspberryPiMB,
		}, nil
	case "radxa_zero3", "radxa-zero3":
		return rootfsSizingConfig{
			enforceFixedSize: false,
		}, nil
	default:
		return rootfsSizingConfig{}, fmt.Errorf("unsupported IMAGE_ID %q; expected one of: raspberry_pi, radxa_zero3", imageID)
	}
}

func pruneRootfsForSizing(rootfsImagePath string, logger *slog.Logger) error {
	mountPoint := filepath.Join(workDir, "rootfs-sizing")
	unmount, err := fuse2fs_mount(rootfsImagePath, mountPoint, 0, logger)
	if err != nil {
		return fmt.Errorf("failed to mount temporary rootfs for pruning: %w", err)
	}
	shouldUnmount := true
	defer func() {
		if shouldUnmount {
			unmount(true)
		}
	}()

	for _, filePath := range ArmbianRootfsRemove {
		fullPath := path.Join(mountPoint, filePath)
		if err := os.RemoveAll(fullPath); err != nil {
			return fmt.Errorf("failed to remove %s during rootfs pre-prune: %w", fullPath, err)
		}
	}

	for _, dirPath := range ArmbianRootFsCreateDirs {
		fullPath := path.Join(mountPoint, dirPath)
		if err := os.MkdirAll(fullPath, 0755); err != nil {
			return fmt.Errorf("failed to create %s during rootfs pre-prune: %w", fullPath, err)
		}
	}

	if err := pruneRootfsUnusedFiles(mountPoint, logger); err != nil {
		return fmt.Errorf("failed to prune temporary rootfs before sizing: %w", err)
	}

	_ = exec.Command("sync").Run()
	unmount(false)
	shouldUnmount = false
	return nil
}

func ensureRootfsPartitionSize(imagePath string, rootPartition part.Partition, logicalBlockSize int64, targetRootSizeMB uint64, logger *slog.Logger) (uint64, error) {
	rootPartSizeBytes := uint64(rootPartition.GetSize())
	targetRootSizeBytes := targetRootSizeMB * 1024 * 1024
	targetRootSizeSectors := targetRootSizeBytes / uint64(logicalBlockSize)
	if targetRootSizeSectors == 0 {
		return 0, fmt.Errorf("invalid rootfs target size: %dMB does not map to any sectors with logical block size %d", targetRootSizeMB, logicalBlockSize)
	}

	if rootPartSizeBytes == targetRootSizeBytes {
		logger.Info("Rootfs already matches fixed partition size", slog.Uint64("target_rootfs_size_bytes", targetRootSizeBytes))
		return targetRootSizeSectors, nil
	}

	if !canRunRootfsResizeTools() {
		return 0, fmt.Errorf("cannot resize rootfs to fixed %dMB target: missing required tools (e2fsck, resize2fs, dumpe2fs)", targetRootSizeMB)
	}

	tmpRootfsPath := filepath.Join(workDir, "rootfs-partition.img")
	_ = os.Remove(tmpRootfsPath)
	defer os.Remove(tmpRootfsPath)

	tmpFile, err := os.Create(tmpRootfsPath)
	if err != nil {
		return 0, fmt.Errorf("failed to create temporary rootfs image: %w", err)
	}

	img, err := diskfs.Open(imagePath)
	if err != nil {
		_ = tmpFile.Close()
		return 0, errors.Join(common.ErrFailedToOpenImage, err)
	}

	readBytes, err := rootPartition.ReadContents(img.Backend, tmpFile)
	_ = img.Close()
	if closeErr := tmpFile.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, fmt.Errorf("failed to extract rootfs partition: %w", err)
	}
	if int64(rootPartSizeBytes) != readBytes {
		return 0, fmt.Errorf("expected to extract %d bytes from rootfs partition, got %d", rootPartSizeBytes, readBytes)
	}
	if rootPartSizeBytes < targetRootSizeBytes {
		if err := os.Truncate(tmpRootfsPath, int64(targetRootSizeBytes)); err != nil {
			return 0, fmt.Errorf("failed to extend temporary rootfs image to target size: %w", err)
		}
	}

	if err := pruneRootfsForSizing(tmpRootfsPath, logger); err != nil {
		return 0, err
	}

	logger.Info("Checking rootfs fit for fixed partition size", slog.String("temp_image", tmpRootfsPath), slog.Uint64("target_rootfs_size_MB", targetRootSizeMB))

	if out, err := exec.Command("e2fsck", "-pf", tmpRootfsPath).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("e2fsck failed before rootfs resize: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Compact filesystem first so extents are relocated toward lower blocks before sizing.
	logger.Info("Compacting rootfs filesystem to minimum")
	if out, err := exec.Command("resize2fs", "-M", tmpRootfsPath).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("resize2fs -M failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("e2fsck", "-pf", tmpRootfsPath).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("e2fsck failed after rootfs compaction: %w: %s", err, strings.TrimSpace(string(out)))
	}

	minOut, err := exec.Command("resize2fs", "-P", tmpRootfsPath).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("resize2fs -P failed: %w: %s", err, strings.TrimSpace(string(minOut)))
	}

	minBlocks, err := parseResize2fsMinBlocks(string(minOut))
	if err != nil {
		return 0, err
	}

	fsInfoOut, err := exec.Command("dumpe2fs", "-h", tmpRootfsPath).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("dumpe2fs failed: %w: %s", err, strings.TrimSpace(string(fsInfoOut)))
	}

	var blockCount uint64
	var blockSize uint64
	scanner := bufio.NewScanner(strings.NewReader(string(fsInfoOut)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "Block count:") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				blockCount, err = strconv.ParseUint(parts[2], 10, 64)
				if err != nil {
					return 0, fmt.Errorf("failed to parse ext4 block count from %q: %w", line, err)
				}
			}
		}
		if strings.HasPrefix(line, "Block size:") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				blockSize, err = strconv.ParseUint(parts[2], 10, 64)
				if err != nil {
					return 0, fmt.Errorf("failed to parse ext4 block size from %q: %w", line, err)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if blockCount == 0 || blockSize == 0 {
		return 0, fmt.Errorf("could not determine ext4 block count/size (block_count=%d block_size=%d)", blockCount, blockSize)
	}

	targetBlocks := targetRootSizeBytes / blockSize
	if targetBlocks == 0 {
		return 0, fmt.Errorf("target rootfs size %d bytes is smaller than ext4 block size %d", targetRootSizeBytes, blockSize)
	}
	if targetBlocks < minBlocks {
		minBytes := minBlocks * blockSize
		return 0, fmt.Errorf("rootfs minimum size (%d bytes) exceeds fixed rootfs target (%d bytes)", minBytes, targetRootSizeBytes)
	}

	if blockCount != targetBlocks {
		logger.Info("Resizing rootfs filesystem to fixed size", slog.Uint64("from_blocks", blockCount), slog.Uint64("to_blocks", targetBlocks), slog.Uint64("block_size", blockSize))
		if out, err := exec.Command("resize2fs", tmpRootfsPath, strconv.FormatUint(targetBlocks, 10)).CombinedOutput(); err != nil {
			return 0, fmt.Errorf("resize2fs failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
	} else {
		logger.Info("Rootfs filesystem already matches fixed target blocks", slog.Uint64("target_blocks", targetBlocks), slog.Uint64("block_size", blockSize))
	}

	if out, err := exec.Command("e2fsck", "-pf", tmpRootfsPath).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("e2fsck failed after rootfs resize: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Truncate(tmpRootfsPath, int64(targetRootSizeBytes)); err != nil {
		return 0, fmt.Errorf("failed to truncate temporary rootfs image to target size: %w", err)
	}

	src, err := os.Open(tmpRootfsPath)
	if err != nil {
		return 0, fmt.Errorf("failed to reopen resized rootfs image: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(imagePath, os.O_RDWR, 0644)
	if err != nil {
		return 0, fmt.Errorf("failed to open image for rootfs writeback: %w", err)
	}
	defer dst.Close()

	rootStart := rootPartition.GetStart()
	if _, err = dst.Seek(int64(rootStart), io.SeekStart); err != nil {
		return 0, fmt.Errorf("failed to seek image for rootfs writeback: %w", err)
	}
	if _, err = io.CopyN(dst, src, int64(targetRootSizeBytes)); err != nil {
		return 0, fmt.Errorf("failed to write back resized rootfs image: %w", err)
	}

	logger.Info("Rootfs resize completed", slog.Uint64("old_size_bytes", rootPartSizeBytes), slog.Uint64("new_size_bytes", targetRootSizeBytes))
	return targetRootSizeSectors, nil
}

func resizeImage(imagePath string, flavour imageFlavour, imageID string, logger *slog.Logger) (*partitions, error) {
	rootfsSizing, err := rootfsSizingConfigForImage(imageID)
	if err != nil {
		return nil, err
	}

	img, partitionTable, logicalBlockSize, err := openImageWithDetectedSectorSize(imagePath, logger)
	if err != nil {
		return nil, errors.Join(common.ErrFailedToOpenImage, err)
	}
	imgPartitions := partitionTable.GetPartitions()
	if len(imgPartitions) == 0 {
		_ = img.Close()
		return nil, errors.New("image has no partitions")
	}

	rootPartition, err := selectRootPartition(partitionTable, logger)
	if err != nil {
		_ = img.Close()
		return nil, err
	}

	rootPartitionStartBytes := rootPartition.GetStart()
	rootPartitionSizeBytes := rootPartition.GetSize()
	if rootPartitionStartBytes < 0 {
		_ = img.Close()
		return nil, fmt.Errorf("invalid rootfs partition start: %d", rootPartitionStartBytes)
	}
	if rootPartitionSizeBytes <= 0 {
		_ = img.Close()
		return nil, fmt.Errorf("invalid rootfs partition size: %d", rootPartitionSizeBytes)
	}
	if rootPartitionStartBytes%logicalBlockSize != 0 {
		_ = img.Close()
		return nil, fmt.Errorf("rootfs partition start (%d) is not aligned to logical block size (%d)", rootPartitionStartBytes, logicalBlockSize)
	}
	if rootPartitionSizeBytes%logicalBlockSize != 0 {
		_ = img.Close()
		return nil, fmt.Errorf("rootfs partition size (%d) is not aligned to logical block size (%d)", rootPartitionSizeBytes, logicalBlockSize)
	}

	rootFsPartitionStart := uint64(rootPartitionStartBytes / logicalBlockSize) // 0 indexed
	rootfsSizeInSectors := uint64(rootPartitionSizeBytes / logicalBlockSize)

	rootPartitionEndBytes := rootPartitionStartBytes + rootPartitionSizeBytes
	if rootPartitionEndBytes > img.Size {
		if img.Size <= rootPartitionStartBytes {
			_ = img.Close()
			return nil, fmt.Errorf("rootfs partition start (%d) is beyond image size (%d)", rootPartitionStartBytes, img.Size)
		}

		availableRootBytes := img.Size - rootPartitionStartBytes
		clampedSectors := uint64(availableRootBytes / logicalBlockSize)
		if clampedSectors == 0 {
			_ = img.Close()
			return nil, fmt.Errorf("rootfs partition has no addressable sectors within image size (start=%d image_size=%d logical_block=%d)", rootPartitionStartBytes, img.Size, logicalBlockSize)
		}

		logger.Warn(
			"Rootfs partition extends beyond image EOF; clamping rootfs span to available bytes",
			slog.Int64("root_start_bytes", rootPartitionStartBytes),
			slog.Int64("root_size_bytes_table", rootPartitionSizeBytes),
			slog.Int64("image_size_bytes", img.Size),
			slog.Uint64("root_size_sectors_clamped", clampedSectors),
		)
		rootfsSizeInSectors = clampedSectors
	}

	sectorsPerMB := uint64(1024 * 1024 / logicalBlockSize)

	if rootfsSizing.enforceFixedSize {
		fixedRootfsSizeSectors, err := ensureRootfsPartitionSize(imagePath, rootPartition, logicalBlockSize, rootfsSizing.fixedSizeMB, logger)
		if err != nil {
			return nil, err
		}
		rootfsSizeInSectors = fixedRootfsSizeSectors
	} else {
		logger.Info(
			"Skipping rootfs fixed-size resize for image",
			slog.String("image_id", imageID),
			slog.Uint64("rootfs_size_bytes", uint64(rootPartition.GetSize())),
			slog.Uint64("rootfs_size_MB", uint64(rootPartition.GetSize())/(1024*1024)),
		)
	}
	img.Close()

	appSizeInSectors := uint64(appPartitionSizeMB * sectorsPerMB)
	dataSizeInSectors := uint64(dataPartitionSizeMB * sectorsPerMB)

	rootPartEnd := uint64(rootFsPartitionStart) + rootfsSizeInSectors - 1
	appPartStart := rootPartEnd + 1
	appPartEnd := appPartStart + appSizeInSectors - 1
	dataPartStart := appPartEnd + 1
	dataPartEnd := dataPartStart + dataSizeInSectors - 1

	lastSector := dataPartEnd

	requiredSizeBytes := (lastSector + 1) * uint64(logicalBlockSize)
	logger.Info(
		"Resizing image",
		slog.String("image_id", imageID),
		slog.Bool("fixed_rootfs_enforced", rootfsSizing.enforceFixedSize),
		slog.Uint64("rootfs_size_MB", (rootfsSizeInSectors*uint64(logicalBlockSize))/(1024*1024)),
		slog.Int("sectors_per_MB", int(sectorsPerMB)),
		"rootfs", fmt.Sprintf("%d - %d", rootFsPartitionStart, rootPartEnd),
		"app_partition", fmt.Sprintf("%d - %d", appPartStart, appPartEnd),
		"data_partition", fmt.Sprintf("%d - %d", dataPartStart, dataPartEnd),
		slog.Uint64("size_MB", requiredSizeBytes/(1024*1024)),
	)
	if err := os.Truncate(imagePath, int64(requiredSizeBytes)); err != nil {
		return nil, errors.Join(common.ErrFailedToResizeImage, err)
	}

	return &partitions{
		size: requiredSizeBytes,
		root: partition{
			start:       uint64(rootFsPartitionStart),
			end:         rootPartEnd,
			sectorCount: rootfsSizeInSectors,
		},
		app: partition{
			start:       appPartStart,
			end:         appPartEnd,
			sectorCount: appSizeInSectors,
		},
		data: partition{
			start:       dataPartStart,
			end:         dataPartEnd,
			sectorCount: dataSizeInSectors,
		},
	}, nil
}

func createPartitions(path string, partitionSpecs *partitions, logger *slog.Logger) error {
	img, table, _, err := openImageWithDetectedSectorSize(path, logger)
	if err != nil {
		return errors.Join(common.ErrFailedToOpenImage, err)
	}
	defer img.Close()

	switch table := table.(type) {
	case *gpt.Table:
		gptTable := table

		newPartitions := gptTable.Partitions
		if len(newPartitions) > 2 {
			newPartitions = newPartitions[:2] // keep only first two partitions, there may be more but with size 0
		}
		if len(newPartitions) >= 2 {
			newPartitions[1].End = partitionSpecs.root.end
		}

		partitionsToAdd := []*gpt.Partition{
			{
				Start: partitionSpecs.app.start,
				End:   partitionSpecs.app.end,
				Type:  gpt.MicrosoftBasicData,
				Name:  constants.AppPartitionLabel,
			},
			{
				Start: partitionSpecs.data.start,
				End:   partitionSpecs.data.end,
				Type:  gpt.MicrosoftBasicData,
				Name:  constants.DataPartitionLabel,
			},
		}

		gptTable.Partitions = append(newPartitions, partitionsToAdd...)
		gptTable.Repair(partitionSpecs.size)

		if err := img.Partition(gptTable); err != nil {
			return errors.Join(common.ErrFailedToWritePartitionTable, err)
		}
	case *mbr.Table:
		mbrTable := table
		partitionsWithNonZeroSize := lo.Filter(mbrTable.Partitions, func(par *mbr.Partition, _ int) bool {
			return par.Size > 0
		})

		if len(partitionsWithNonZeroSize) > 2 {
			return errors.New("MBR table already has more than 2 partitions defined, cannot add 2 more for TezSign")
		}

		newPartitions := mbrTable.Partitions
		if len(newPartitions) > 2 {
			newPartitions = newPartitions[:2] // keep only first two partitions, there may be more but with size 0
		}
		if len(newPartitions) >= 2 {
			newPartitions[1].Size = uint32(partitionSpecs.root.sectorCount)
		}

		partitionsToAdd := []*mbr.Partition{
			{
				Bootable: false,
				Type:     mbr.Linux,
				Start:    uint32(partitionSpecs.app.start),
				Size:     uint32(partitionSpecs.app.sectorCount),
			},
			{
				Bootable: false,
				Type:     mbr.Linux,
				Start:    uint32(partitionSpecs.data.start),
				Size:     uint32(partitionSpecs.data.sectorCount),
			},
		}

		mbrTable.Partitions = append(newPartitions, partitionsToAdd...)
		mbrTable.Repair(partitionSpecs.size)

		if err := img.Partition(mbrTable); err != nil {
			return errors.Join(common.ErrFailedToWritePartitionTable, err)
		}
	default:
		return errors.Join(common.ErrFailedToPartitionImage, common.ErrPartitionTableNotGPT)
	}

	return nil
}

func formatPartitionTable(path string, flavour imageFlavour, logger *slog.Logger) error {
	img, table, _, err := openImageWithDetectedSectorSize(path, logger)
	if err != nil {
		return errors.Join(common.ErrFailedToOpenImage, err)
	}
	defer img.Close()

	partitions := table.GetPartitions()
	appPartitionIndex := len(partitions) - 2
	dataPartitionIndex := len(partitions) - 1
	logger.Info("Formatting partitions", slog.Int("app_partition_index", appPartitionIndex), slog.Int("data_partition_index", dataPartitionIndex))

	appPartition := partitions[appPartitionIndex]
	dataPartition := partitions[dataPartitionIndex]

	appPartitionOffset := int64(appPartition.GetStart())
	dataPartitionOffset := int64(dataPartition.GetStart())
	appPartitionSize := int64(appPartition.GetSize())
	dataPartitionSize := int64(dataPartition.GetSize())

	// mkfs.ext4 -E offset=104857600,root_owner=1000:1000 -F disk.img 51200K
	slog.Info("Formatting app and data partitions", slog.Int64("app_offset", appPartitionOffset), slog.Int64("app_size", appPartitionSize), slog.Int64("data_offset", dataPartitionOffset), slog.Int64("data_size", dataPartitionSize))
	if err := exec.Command("mkfs.ext4", "-E", fmt.Sprintf("offset=%d", appPartitionOffset), "-F", path, fmt.Sprintf("%dK", appPartitionSize/1024), "-L", constants.AppPartitionLabel).Run(); err != nil {
		return errors.Join(common.ErrFailedToFormatPartition, err)
	}

	slog.Info("Formatting data partition", slog.Int64("data_offset", dataPartitionOffset), slog.Int64("data_size", dataPartitionSize))
	if err := exec.Command("mkfs.ext4", "-J", "size=8", "-m", "0", "-I", "1024", "-O", "inline_data,fast_commit", "-E", fmt.Sprintf("offset=%d", dataPartitionOffset), "-F", path, fmt.Sprintf("%dK", dataPartitionSize/1024), "-L", constants.DataPartitionLabel).Run(); err != nil {
		return errors.Join(common.ErrFailedToFormatPartition, err)
	}

	return nil
}

func PartitionImage(path string, flavour imageFlavour, imageID string, logger *slog.Logger) error {
	partitionSpecs, err := resizeImage(path, flavour, imageID, logger)
	if err != nil {
		return errors.Join(common.ErrFailedToPartitionImage, err)
	}

	if err := createPartitions(path, partitionSpecs, logger); err != nil {
		return errors.Join(common.ErrFailedToPartitionImage, err)
	}

	if err := formatPartitionTable(path, flavour, logger); err != nil {
		return errors.Join(common.ErrFailedToPartitionImage, err)
	}

	logger.Info("✅ Successfully added TezSign partitions to the image.")
	return nil
}
