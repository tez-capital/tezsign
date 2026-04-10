package common

import (
	"errors"

	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/partition/gpt"
	"github.com/diskfs/go-diskfs/partition/mbr"
	"github.com/diskfs/go-diskfs/partition/part"
	"github.com/tez-capital/tezsign/tools/constants"
)

func GetTezsignPartitions(img *disk.Disk) (boot, rootfs, app, data part.Partition, err error) {
	table, err := img.GetPartitionTable()
	if err != nil {
		return nil, nil, nil, nil, errors.Join(ErrFailedToOpenPartitionTable, err)
	}

	var bootPartition part.Partition
	var rootfsPartition part.Partition
	var appPartition part.Partition
	var dataPartition part.Partition

	switch table := table.(type) {
	case *gpt.Table:
		gptTable := table
		if len(gptTable.Partitions) < 3 {
			return nil, nil, nil, nil, errors.Join(ErrFailedToConfigureImage, ErrUnexpectedPartitionCount)
		}
		for _, partition := range gptTable.Partitions {
			switch partition.Name {
			case "boot", "bootfs":
				bootPartition = partition
			case "root", "rootfs":
				rootfsPartition = partition
			case constants.AppPartitionLabel:
				appPartition = partition
			case constants.DataPartitionLabel:
				dataPartition = partition
			}
		}
	case *mbr.Table:
		mbrTable := table
		parts := make([]part.Partition, 0, len(mbrTable.Partitions))
		for _, p := range mbrTable.Partitions {
			if p == nil || p.Size == 0 {
				continue
			}
			parts = append(parts, p)
		}
		switch len(parts) {
		case 3:
			bootPartition = parts[0]
			appPartition = parts[1]
			dataPartition = parts[2]
		case 4:
			bootPartition = parts[0]
			rootfsPartition = parts[1]
			appPartition = parts[2]
			dataPartition = parts[3]
		default:
			return nil, nil, nil, nil, errors.Join(ErrFailedToConfigureImage, ErrUnexpectedPartitionCount)
		}
	default:
		return nil, nil, nil, nil, errors.Join(ErrFailedToPartitionImage, ErrPartitionTableNotGPT)
	}

	if appPartition == nil || dataPartition == nil {
		return nil, nil, nil, nil, errors.Join(ErrFailedToConfigureImage, ErrUnexpectedPartitionCount)
	}
	return bootPartition, rootfsPartition, appPartition, dataPartition, nil
}
