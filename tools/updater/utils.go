package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/partition/part"
	"github.com/tez-capital/tezsign/tools/common"
)

func loadImage(path string, mode diskfs.OpenModeOption) (*disk.Disk, part.Partition, part.Partition, part.Partition, error) {
	flags := os.O_RDONLY
	if mode != diskfs.ReadOnly {
		flags = os.O_RDWR
	}

	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to open device %s: %w", path, err)
	}

	disk, err := diskfs.OpenBackend(file.New(f, false), diskfs.WithOpenMode(mode), diskfs.WithSectorSize(diskfs.SectorSizeDefault))
	if err != nil {
		f.Close()
		return nil, nil, nil, nil, errors.New("failed to open disk backend")
	}

	bootPartition, rootfsPartition, appPartition, _, err := common.GetTezsignPartitions(disk)
	if err != nil {
		disk.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to read partitions from the device: %w", err)
	}

	return disk, bootPartition, rootfsPartition, appPartition, nil
}

func filesystemForPartition(d *disk.Disk, p part.Partition) (filesystem.FileSystem, error) {
	table, err := d.GetPartitionTable()
	if err != nil {
		return nil, err
	}
	parts := table.GetPartitions()
	for idx, part := range parts {
		if part == p {
			return d.GetFilesystem(idx + 1)
		}
		// fallback to matching start/size when pointer comparison fails
		if part != nil && p != nil && part.GetStart() == p.GetStart() && part.GetSize() == p.GetSize() {
			return d.GetFilesystem(idx + 1)
		}
	}
	return nil, errors.New("partition not found for filesystem lookup")
}
