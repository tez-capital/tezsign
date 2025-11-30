package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

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

func resizeImage(imagePath string, flavour imageFlavour, logger *slog.Logger) (*partitions, error) {
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
	img.Close()

	appSizeInSectors := uint64(appPartitionSizeMB * sectorsPerMB)
	dataSizeInSectors := uint64(dataPartitionSizeMB * sectorsPerMB)

	rootPartEnd := uint64(rootFsPartitionStart) + rootfsSizeInSectors
	appPartStart := rootPartEnd + 1
	appPartEnd := appPartStart + appSizeInSectors
	dataPartStart := appPartEnd + 1
	dataPartEnd := dataPartStart + dataSizeInSectors

	lastSector := dataPartEnd

	requiredSizeBytes := lastSector * uint64(logicalBlockSize)
	logger.Info("Resizing image", slog.Int("sectors_per_MB", int(sectorsPerMB)), "rootfs", fmt.Sprintf("%d - %d", rootFsPartitionStart, rootPartEnd), "app_partition", fmt.Sprintf("%d - %d", appPartStart, appPartEnd), "data_partition", fmt.Sprintf("%d - %d", dataPartStart, dataPartEnd), slog.Uint64("size_MB", requiredSizeBytes/(1024*1024)))
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
	if err := exec.Command("mkfs.ext4", "-E", fmt.Sprintf("offset=%d", dataPartitionOffset), "-F", path, fmt.Sprintf("%dK", dataPartitionSize/1024), "-L", constants.DataPartitionLabel).Run(); err != nil {
		return errors.Join(common.ErrFailedToFormatPartition, err)
	}

	return nil
}

func PartitionImage(path string, flavour imageFlavour, logger *slog.Logger) error {
	partitionSpecs, err := resizeImage(path, flavour, logger)
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
