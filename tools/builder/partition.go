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

func rootfsTargetSizeMBForImage(imageID string) (uint64, error) {
	switch normalizeImageID(imageID) {
	case "raspberry_pi":
		return rootfsPartitionSizeRaspberryPiMB, nil
	case "radxa_zero3", "radxa-zero3":
		return rootfsPartitionSizeRadxaZero3MB, nil
	default:
		return 0, fmt.Errorf("unsupported IMAGE_ID %q; expected one of: raspberry_pi, radxa_zero3", imageID)
	}
}

func pruneRootfsForSizing(rootfsImagePath string, logger *slog.Logger) error {
	mountPoint := filepath.Join(workDir, "rootfs-sizing")
	unmount, err := fuse2fs_mount(rootfsImagePath, mountPoint, 0, logger)
	if err != nil {
		return fmt.Errorf("failed to mount temporary rootfs for pruning: %w", err)
	}
	defer unmount(true)

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

	unmount(false)
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
	targetRootfsSizeMB, err := rootfsTargetSizeMBForImage(imageID)
	if err != nil {
		return nil, err
	}

	img, err := diskfs.Open(imagePath)
	if err != nil {
		return nil, errors.Join(common.ErrFailedToOpenImage, err)
	}

	partitionTable, err := img.GetPartitionTable()
	if err != nil {
		return nil, errors.Join(common.ErrFailedToOpenPartitionTable, err)
	}

	logicalBlockSize := img.LogicalBlocksize
	if logicalBlockSize == 0 {
		// Fallback in case the library can't determine the size
		logicalBlockSize = 512
		logger.Warn("Could not determine block size, falling back to 512.")
	}
	imgPartitions := partitionTable.GetPartitions()
	var rootPartition part.Partition
	if len(imgPartitions) > 1 {
		rootPartition = imgPartitions[1] // second partition is rootfs
	} else {
		rootPartition = imgPartitions[0] // fallback to first partition if only one exists e.g. radxa with binman
	}

	rootFsPartitionStart := rootPartition.GetStart() / logicalBlockSize // 0 indexed
	rootfsSizeInSectors := uint64(rootPartition.GetSize() / logicalBlockSize)
	sectorsPerMB := uint64(1024 * 1024 / logicalBlockSize)
	if fixedRootfsSizeSectors, err := ensureRootfsPartitionSize(imagePath, rootPartition, logicalBlockSize, targetRootfsSizeMB, logger); err != nil {
		return nil, err
	} else {
		rootfsSizeInSectors = fixedRootfsSizeSectors
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
		slog.Uint64("target_rootfs_size_MB", targetRootfsSizeMB),
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

func createPartitions(path string, partitionSpecs *partitions) error {
	img, err := diskfs.Open(path)
	if err != nil {
		return errors.Join(common.ErrFailedToOpenImage, err)
	}
	defer img.Close()

	table, err := img.GetPartitionTable()
	if err != nil {
		return errors.Join(common.ErrFailedToOpenPartitionTable, err)
	}
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
	img, err := diskfs.Open(path)
	if err != nil {
		return errors.Join(common.ErrFailedToOpenImage, err)
	}
	defer img.Close()

	table, err := img.GetPartitionTable()
	if err != nil {
		return errors.Join(common.ErrFailedToOpenPartitionTable, err)
	}

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

	if err := createPartitions(path, partitionSpecs); err != nil {
		return errors.Join(common.ErrFailedToPartitionImage, err)
	}

	if err := formatPartitionTable(path, flavour, logger); err != nil {
		return errors.Join(common.ErrFailedToPartitionImage, err)
	}

	logger.Info("✅ Successfully added TezSign partitions to the image.")
	return nil
}
