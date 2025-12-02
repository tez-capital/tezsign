package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/partition"
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

	idx, err := partitionIndex(table, p)
	if err != nil {
		return nil, err
	}

	return d.GetFilesystem(idx)
}

func mountAppPartition(writable bool) (string, func(), error) {
	if err := ensureMountAvailable(); err != nil {
		return "", nil, err
	}

	appDev, err := filepath.EvalSymlinks("/dev/disk/by-label/app")
	if err != nil {
		return "", nil, fmt.Errorf("failed to resolve /dev/disk/by-label/app: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "tezsign_app_mount_")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp mount dir: %w", err)
	}

	opts := "ro,noload"
	if writable {
		opts = "rw,sync"
	}
	mountCmd := exec.Command("mount", "-o", opts, appDev, tmpDir)
	if out, err := mountCmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return "", nil, fmt.Errorf("failed to mount app partition (%s): %v: %s", appDev, err, string(out))
	}

	cleanup := func() {
		exec.Command("umount", tmpDir).Run()
		os.RemoveAll(tmpDir)
	}
	return tmpDir, cleanup, nil
}

func mountSpecificPartition(devicePath string, partIndex int, writable bool) (string, func(), error) {
	if err := ensureMountAvailable(); err != nil {
		return "", nil, err
	}

	partDevice := partitionDevicePath(devicePath, partIndex)
	if _, err := os.Stat(partDevice); err != nil {
		return "", nil, fmt.Errorf("resolved partition device does not exist: %s: %w", partDevice, err)
	}

	tmpDir, err := os.MkdirTemp("", "tezsign_mount_")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp mount dir: %w", err)
	}

	opts := "ro,noload"
	if writable {
		opts = "rw,sync"
	}
	mountCmd := exec.Command("mount", "-o", opts, partDevice, tmpDir)
	if out, err := mountCmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return "", nil, fmt.Errorf("failed to mount partition (%s): %v: %s", partDevice, err, string(out))
	}

	cleanup := func() {
		exec.Command("umount", tmpDir).Run()
		os.RemoveAll(tmpDir)
	}
	return tmpDir, cleanup, nil
}

func partitionIndex(tbl partition.Table, target part.Partition) (int, error) {
	parts := tbl.GetPartitions()
	for idx, part := range parts {
		if part == target {
			return idx + 1, nil
		}
		if part != nil && target != nil && part.GetStart() == target.GetStart() && part.GetSize() == target.GetSize() {
			return idx + 1, nil
		}
	}
	return 0, errors.New("partition not found for filesystem lookup")
}

func partitionDevicePath(device string, index int) string {
	// mmcblk devices need a 'p' before the partition number; sd/loop don't.
	sep := ""
	if len(device) > 0 && device[len(device)-1] >= '0' && device[len(device)-1] <= '9' {
		sep = "p"
	}
	return fmt.Sprintf("%s%s%d", device, sep, index)
}
