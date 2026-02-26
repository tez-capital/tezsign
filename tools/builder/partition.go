package main

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
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

func normalizeImageID(imageID string) string {
	base := strings.ToLower(strings.TrimSpace(imageID))
	return strings.TrimSuffix(base, ".dev")
}

type rootfsSizingConfig struct {
	fixedSizeMB uint64
}

type rootfsFilesystemMetadata struct {
	uuid  string
	label string
}

type rootfsBackup struct {
	backupDir string
	metadata  rootfsFilesystemMetadata
}

func rootfsSizingConfigForImage(imageID string) (rootfsSizingConfig, error) {
	switch normalizeImageID(imageID) {
	case "raspberry_pi":
		return rootfsSizingConfig{
			fixedSizeMB: rootfsPartitionSizeRaspberryPiZeroMB,
		}, nil
	case "radxa_zero3", "radxa-zero3":
		return rootfsSizingConfig{
			fixedSizeMB: rootfsPartitionSizeRadxaZero3MB,
		}, nil
	default:
		return rootfsSizingConfig{}, fmt.Errorf("unsupported IMAGE_ID %q; expected one of: raspberry_pi, radxa_zero3", imageID)
	}
}

func findRootPartitionInImage(imagePath string, logger *slog.Logger) (part.Partition, error) {
	img, table, _, err := openImageWithDetectedSectorSize(imagePath, logger)
	if err != nil {
		return nil, errors.Join(common.ErrFailedToOpenImage, err)
	}
	defer img.Close()

	rootPartition, err := selectRootPartition(table, logger)
	if err != nil {
		return nil, err
	}
	if rootPartition == nil {
		return nil, errors.New("root partition is nil")
	}
	if rootPartition.GetStart() < 0 {
		return nil, fmt.Errorf("invalid root partition start offset: %d", rootPartition.GetStart())
	}
	if rootPartition.GetSize() <= 0 {
		return nil, fmt.Errorf("invalid root partition size: %d", rootPartition.GetSize())
	}

	return rootPartition, nil
}

func captureRootfsFilesystemMetadata(imagePath string, rootPartition part.Partition, logger *slog.Logger) (rootfsFilesystemMetadata, error) {
	var metadata rootfsFilesystemMetadata

	if _, err := exec.LookPath("blkid"); err != nil {
		logger.Warn("blkid is not available; rootfs filesystem UUID/label will not be preserved", slog.Any("error", err))
		return metadata, nil
	}

	out, err := exec.Command(
		"blkid",
		"-p",
		"-o", "export",
		"-O", strconv.FormatInt(rootPartition.GetStart(), 10),
		"-S", strconv.FormatInt(rootPartition.GetSize(), 10),
		imagePath,
	).CombinedOutput()
	if err != nil {
		return metadata, fmt.Errorf("failed to probe rootfs metadata with blkid: %w: %s", err, strings.TrimSpace(string(out)))
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		switch parts[0] {
		case "UUID":
			metadata.uuid = strings.TrimSpace(parts[1])
		case "LABEL":
			metadata.label = strings.TrimSpace(parts[1])
		}
	}
	if err := scanner.Err(); err != nil {
		return metadata, fmt.Errorf("failed to parse blkid output: %w", err)
	}

	logger.Info(
		"Captured rootfs filesystem metadata",
		slog.String("uuid", metadata.uuid),
		slog.String("label", metadata.label),
	)
	return metadata, nil
}

func pruneRootfsBeforeBackup(rootfs string, flavour imageFlavour, logger *slog.Logger) error {
	removePaths := make([]string, 0, len(ArmbianRootfsRemove)+len(DevArmbianRootfsRemove))
	removePaths = append(removePaths, ArmbianRootfsRemove...)
	if flavour == DevImage {
		removePaths = append(removePaths, DevArmbianRootfsRemove...)
	}

	for _, filePath := range removePaths {
		fullPath := filepath.Join(rootfs, strings.TrimPrefix(filePath, "/"))
		if err := os.RemoveAll(fullPath); err != nil {
			return fmt.Errorf("failed to remove %s before backup: %w", fullPath, err)
		}
	}

	for _, dirPath := range ArmbianRootFsCreateDirs {
		fullPath := filepath.Join(rootfs, strings.TrimPrefix(dirPath, "/"))
		if err := os.MkdirAll(fullPath, 0755); err != nil {
			return fmt.Errorf("failed to create %s before backup: %w", fullPath, err)
		}
	}

	if err := pruneRootfsUnusedFiles(rootfs, logger); err != nil {
		return fmt.Errorf("failed to prune rootfs before backup: %w", err)
	}
	return nil
}

func backupRootfsWithRsync(imagePath string, rootPartition part.Partition, flavour imageFlavour, logger *slog.Logger) (string, error) {
	if _, err := exec.LookPath("rsync"); err != nil {
		return "", fmt.Errorf("rsync is required for rootfs migration but was not found: %w", err)
	}

	mountPoint := filepath.Join(workDir, "rootfs-rsync-source")
	backupDir := filepath.Join(workDir, "rootfs-rsync-backup")

	_ = os.RemoveAll(mountPoint)
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return "", fmt.Errorf("failed to create rootfs source mount point %s: %w", mountPoint, err)
	}
	_ = os.RemoveAll(backupDir)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create rootfs backup directory %s: %w", backupDir, err)
	}

	unmount, err := fuse2fs_mount(imagePath, mountPoint, int(rootPartition.GetStart()), logger)
	if err != nil {
		return "", fmt.Errorf("failed to mount rootfs for backup: %w", err)
	}
	shouldUnmount := true
	defer func() {
		if shouldUnmount {
			unmount(true)
		}
	}()

	if err := pruneRootfsBeforeBackup(mountPoint, flavour, logger); err != nil {
		return "", err
	}

	out, err := exec.Command("rsync", "-aH", "--numeric-ids", mountPoint+"/", backupDir+"/").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to rsync rootfs to backup directory: %w: %s", err, strings.TrimSpace(string(out)))
	}

	_ = exec.Command("sync").Run()
	unmount(false)
	shouldUnmount = false

	logger.Info("Backed up rootfs to temporary directory", slog.String("path", backupDir))
	return backupDir, nil
}

func backupRootfsBeforeRepartition(imagePath string, flavour imageFlavour, logger *slog.Logger) (*rootfsBackup, error) {
	rootPartition, err := findRootPartitionInImage(imagePath, logger)
	if err != nil {
		return nil, err
	}

	metadata, err := captureRootfsFilesystemMetadata(imagePath, rootPartition, logger)
	if err != nil {
		logger.Warn("Failed to capture rootfs filesystem metadata; proceeding with defaults", slog.Any("error", err))
		metadata = rootfsFilesystemMetadata{}
	}

	backupDir, err := backupRootfsWithRsync(imagePath, rootPartition, flavour, logger)
	if err != nil {
		return nil, err
	}

	return &rootfsBackup{
		backupDir: backupDir,
		metadata:  metadata,
	}, nil
}

func restoreRootfsFromBackup(imagePath string, backupDir string, logger *slog.Logger) error {
	if _, err := exec.LookPath("rsync"); err != nil {
		return fmt.Errorf("rsync is required for rootfs migration but was not found: %w", err)
	}

	rootPartition, err := findRootPartitionInImage(imagePath, logger)
	if err != nil {
		return err
	}

	mountPoint := filepath.Join(workDir, "rootfs-rsync-target")
	_ = os.RemoveAll(mountPoint)
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return fmt.Errorf("failed to create rootfs target mount point %s: %w", mountPoint, err)
	}

	unmount, err := fuse2fs_mount(imagePath, mountPoint, int(rootPartition.GetStart()), logger)
	if err != nil {
		return fmt.Errorf("failed to mount recreated rootfs for restore: %w", err)
	}
	shouldUnmount := true
	defer func() {
		if shouldUnmount {
			unmount(true)
		}
	}()

	out, err := exec.Command("rsync", "-aH", "--numeric-ids", backupDir+"/", mountPoint+"/").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to restore rootfs from backup directory: %w: %s", err, strings.TrimSpace(string(out)))
	}

	_ = exec.Command("sync").Run()
	unmount(false)
	shouldUnmount = false
	return nil
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
	if rootPartitionStartBytes < 0 {
		_ = img.Close()
		return nil, fmt.Errorf("invalid rootfs partition start: %d", rootPartitionStartBytes)
	}
	if rootPartitionStartBytes%logicalBlockSize != 0 {
		_ = img.Close()
		return nil, fmt.Errorf("rootfs partition start (%d) is not aligned to logical block size (%d)", rootPartitionStartBytes, logicalBlockSize)
	}
	if img.Size <= rootPartitionStartBytes {
		_ = img.Close()
		return nil, fmt.Errorf("rootfs partition start (%d) is beyond image size (%d)", rootPartitionStartBytes, img.Size)
	}

	rootFsPartitionStart := uint64(rootPartitionStartBytes / logicalBlockSize) // 0 indexed
	targetRootSizeBytes := rootfsSizing.fixedSizeMB * 1024 * 1024
	if targetRootSizeBytes == 0 {
		_ = img.Close()
		return nil, fmt.Errorf("invalid rootfs target size: %dMB", rootfsSizing.fixedSizeMB)
	}
	if targetRootSizeBytes%uint64(logicalBlockSize) != 0 {
		_ = img.Close()
		return nil, fmt.Errorf("rootfs target size %d bytes is not aligned to logical block size %d", targetRootSizeBytes, logicalBlockSize)
	}
	rootfsSizeInSectors := targetRootSizeBytes / uint64(logicalBlockSize)

	sectorsPerMB := uint64(1024 * 1024 / logicalBlockSize)
	_ = img.Close()

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
		rootPartition, err := selectRootPartition(gptTable, logger)
		if err != nil {
			return fmt.Errorf("failed to select GPT root partition while creating partitions: %w", err)
		}
		rootGPTPartition, ok := rootPartition.(*gpt.Partition)
		if !ok {
			return fmt.Errorf("selected GPT root partition has unexpected type %T", rootPartition)
		}

		// Keep existing non-TezSign partitions, but always resize the actual root partition.
		newPartitions := make([]*gpt.Partition, 0, len(gptTable.Partitions)+2)
		for _, p := range gptTable.Partitions {
			if p == nil {
				continue
			}
			if p == rootGPTPartition {
				p.End = partitionSpecs.root.end
				// Ensure go-diskfs can reconcile Start/End/Size during GPT serialization.
				p.Size = 0
				newPartitions = append(newPartitions, p)
				continue
			}
			if p.Name == constants.AppPartitionLabel || p.Name == constants.DataPartitionLabel {
				continue
			}
			newPartitions = append(newPartitions, p)
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

		rootPartition, err := selectRootPartition(mbrTable, logger)
		if err != nil {
			return fmt.Errorf("failed to select MBR root partition while creating partitions: %w", err)
		}
		rootMBRPartition, ok := rootPartition.(*mbr.Partition)
		if !ok {
			return fmt.Errorf("selected MBR root partition has unexpected type %T", rootPartition)
		}

		newPartitions := make([]*mbr.Partition, 0, len(mbrTable.Partitions)+2)
		for _, p := range mbrTable.Partitions {
			if p == nil || p.Size == 0 {
				continue
			}
			if p == rootMBRPartition {
				p.Size = uint32(partitionSpecs.root.sectorCount)
				newPartitions = append(newPartitions, p)
				continue
			}
			newPartitions = append(newPartitions, p)
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

func formatPartitionTable(path string, flavour imageFlavour, rootfsMetadata rootfsFilesystemMetadata, logger *slog.Logger) error {
	_ = flavour
	img, table, _, err := openImageWithDetectedSectorSize(path, logger)
	if err != nil {
		return errors.Join(common.ErrFailedToOpenImage, err)
	}
	defer img.Close()

	rootPartition, err := selectRootPartition(table, logger)
	if err != nil {
		return errors.Join(common.ErrFailedToFormatPartition, err)
	}

	partitions := table.GetPartitions()
	if len(partitions) < 2 {
		return fmt.Errorf("unexpected partition count %d while formatting image", len(partitions))
	}
	appPartitionIndex := len(partitions) - 2
	dataPartitionIndex := len(partitions) - 1
	logger.Info("Formatting partitions", slog.Int("app_partition_index", appPartitionIndex), slog.Int("data_partition_index", dataPartitionIndex))

	appPartition := partitions[appPartitionIndex]
	dataPartition := partitions[dataPartitionIndex]

	rootPartitionOffset := int64(rootPartition.GetStart())
	rootPartitionSize := int64(rootPartition.GetSize())
	appPartitionOffset := int64(appPartition.GetStart())
	dataPartitionOffset := int64(dataPartition.GetStart())
	appPartitionSize := int64(appPartition.GetSize())
	dataPartitionSize := int64(dataPartition.GetSize())

	rootMkfsArgs := []string{
		"-E", fmt.Sprintf("offset=%d", rootPartitionOffset),
		"-F", path,
		fmt.Sprintf("%dK", rootPartitionSize/1024),
	}
	if rootfsMetadata.uuid != "" {
		rootMkfsArgs = append(rootMkfsArgs, "-U", rootfsMetadata.uuid)
	}
	if rootfsMetadata.label != "" {
		rootMkfsArgs = append(rootMkfsArgs, "-L", rootfsMetadata.label)
	}
	slog.Info(
		"Formatting root partition",
		slog.Int64("root_offset", rootPartitionOffset),
		slog.Int64("root_size", rootPartitionSize),
		slog.String("root_uuid", rootfsMetadata.uuid),
		slog.String("root_label", rootfsMetadata.label),
	)
	if out, err := exec.Command("mkfs.ext4", rootMkfsArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to format root partition: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// mkfs.ext4 -E offset=104857600,root_owner=1000:1000 -F disk.img 51200K
	slog.Info("Formatting app and data partitions", slog.Int64("app_offset", appPartitionOffset), slog.Int64("app_size", appPartitionSize), slog.Int64("data_offset", dataPartitionOffset), slog.Int64("data_size", dataPartitionSize))
	if out, err := exec.Command("mkfs.ext4", "-E", fmt.Sprintf("offset=%d", appPartitionOffset), "-F", path, fmt.Sprintf("%dK", appPartitionSize/1024), "-L", constants.AppPartitionLabel).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to format app partition: %w: %s", err, strings.TrimSpace(string(out)))
	}

	slog.Info("Formatting data partition", slog.Int64("data_offset", dataPartitionOffset), slog.Int64("data_size", dataPartitionSize))
	if out, err := exec.Command("mkfs.ext4", "-J", "size=8", "-m", "0", "-I", "1024", "-O", "inline_data,fast_commit", "-E", fmt.Sprintf("offset=%d", dataPartitionOffset), "-F", path, fmt.Sprintf("%dK", dataPartitionSize/1024), "-L", constants.DataPartitionLabel).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to format data partition: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

func PartitionImage(path string, flavour imageFlavour, imageID string, logger *slog.Logger) error {
	if _, err := rootfsSizingConfigForImage(imageID); err != nil {
		return errors.Join(common.ErrFailedToPartitionImage, err)
	}

	rootfsBackup, err := backupRootfsBeforeRepartition(path, flavour, logger)
	if err != nil {
		return errors.Join(common.ErrFailedToPartitionImage, err)
	}
	defer os.RemoveAll(rootfsBackup.backupDir)

	partitionSpecs, err := resizeImage(path, flavour, imageID, logger)
	if err != nil {
		return errors.Join(common.ErrFailedToPartitionImage, err)
	}

	if err := createPartitions(path, partitionSpecs, logger); err != nil {
		return errors.Join(common.ErrFailedToPartitionImage, err)
	}

	if err := formatPartitionTable(path, flavour, rootfsBackup.metadata, logger); err != nil {
		return errors.Join(common.ErrFailedToPartitionImage, err)
	}

	if err := restoreRootfsFromBackup(path, rootfsBackup.backupDir, logger); err != nil {
		return errors.Join(common.ErrFailedToPartitionImage, err)
	}

	logger.Info("✅ Successfully added TezSign partitions to the image.")
	return nil
}
