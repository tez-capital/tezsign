package main

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/partition/part"
	"github.com/tez-capital/tezsign/tools/common"
	"github.com/tez-capital/tezsign/tools/constants"
)

const (
	BOOT_PARTITION_NUM   = 1
	ROOTFS_PARTITION_NUM = 2
)

func serializeOverlays(overlays []string) string {
	overlaysWithOptions := []string{}
	for _, overlay := range overlays {
		options, ok := ArmbianActivateOverlays[overlay]
		if ok && options != "" {
			overlaysWithOptions = append(overlaysWithOptions, fmt.Sprintf("%s,%s", overlay, options))
		} else {
			overlaysWithOptions = append(overlaysWithOptions, overlay)
		}
	}

	return strings.Join(overlaysWithOptions, " ")
}

func patchArmbianEnvTxt(bootMountPoint string, availableOverlays map[string]string, logger *slog.Logger) error {
	armbianEnvTxtPath := path.Join(bootMountPoint, "armbianEnv.txt")

	if _, err := os.Stat(armbianEnvTxtPath); err != nil {
		return err
	}
	userOverlayDir := path.Join(bootMountPoint, "overlay-user")

	if err := os.MkdirAll(userOverlayDir, 0755); err != nil {
		return fmt.Errorf("failed to create overlay-user directory: %w", err)
	}

	// copy overlays to overlay-user/
	for overlayName, overlayPath := range availableOverlays {
		destPath := path.Join(userOverlayDir, overlayName+".dtbo")
		logger.Info("Copying dtbo file to overlay-user", slog.String("src", overlayPath), slog.String("dst", destPath))
		input, err := os.ReadFile(overlayPath)
		if err != nil {
			return fmt.Errorf("failed to read overlay file %s: %w", overlayPath, err)
		}
		err = os.WriteFile(destPath, input, 0644)
		if err != nil {
			return fmt.Errorf("failed to write overlay file %s: %w", destPath, err)
		}
	}

	overlays := serializeOverlays(slices.Collect(maps.Keys(availableOverlays)))

	logger.Info("Patching armbianEnv.txt", slog.String("path", armbianEnvTxtPath), slog.String("overlays", overlays))
	err := EditTxtFile(armbianEnvTxtPath, []Edit{
		{Key: "user_overlays", Value: overlays},
	})
	if err != nil {
		return fmt.Errorf("failed to edit armbianEnv.txt: %w", err)
	}

	return nil
}

func patchConfigTxt(bootMountPoint string, availableOverlays map[string]string, logger *slog.Logger) error {
	configTxtPath := path.Join(bootMountPoint, "config.txt")
	if _, err := os.Stat(configTxtPath); err != nil {
		return err
	}

	err := EditTxtFile(configTxtPath, []Edit{
		{Key: "otg_mode", Value: "0"},
	})
	if err != nil {
		return fmt.Errorf("failed to edit config.txt: %w", err)
	}

	// Build the exact dtoverlay lines (one per overlay)
	var dtoLines []string
	for _, name := range slices.Collect(maps.Keys(availableOverlays)) {
		// If you have options in ArmbianActivateOverlays map, apply them here
		if opts, ok := ArmbianActivateOverlays[name]; ok && opts != "" {
			// no spaces around commas!
			dtoLines = append(dtoLines, fmt.Sprintf("dtoverlay=%s,%s", name, opts))
		} else {
			dtoLines = append(dtoLines, fmt.Sprintf("dtoverlay=%s", name))
		}
	}

	// Read, remove existing dtoverlay= lines, append our clean ones
	b, err := os.ReadFile(configTxtPath)
	if err != nil {
		return fmt.Errorf("read config.txt: %w", err)
	}
	lines := strings.Split(string(b), "\n")
	out := make([]string, 0, len(lines)+len(dtoLines))
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "dtoverlay=") {
			continue // drop any existing overlay lines
		}
		out = append(out, ln)
	}
	out = append(out, dtoLines...)

	newContent := strings.Join(out, "\n")
	if err := os.WriteFile(configTxtPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("write config.txt: %w", err)
	}

	logger.Info("Patching config.txt (Pi firmware)", slog.String("path", configTxtPath), slog.Any("dtoverlay_lines", dtoLines))
	return nil
}

func patchBootConfiguration(mountPoint string, flavour imageFlavour, logger *slog.Logger) error {
	availableOverlays := map[string]string{}
	err := filepath.Walk(mountPoint, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			logger.Error("Error accessing path", slog.String("path", path), "error", err)
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".dtbo") {
			return nil
		}
		overlayName := strings.TrimSuffix(info.Name(), ".dtbo")
		if _, exists := ArmbianActivateOverlays[overlayName]; exists {
			if _, exists := availableOverlays[overlayName]; exists {
				return nil
			}
			availableOverlays[overlayName] = path
			logger.Debug("Found dtbo file", slog.String("path", path))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to read dtbo files from boot mount point: %w", err)
	}

	armbianEnvTxtPath := path.Join(mountPoint, "armbianEnv.txt")
	if _, err := os.Stat(armbianEnvTxtPath); err == nil { // armbianEnv.txt exists -> patch it
		if err = patchArmbianEnvTxt(mountPoint, availableOverlays, logger); err != nil {
			return fmt.Errorf("failed to patch armbianEnv.txt: %w", err)
		}
	}

	configTxtPath := path.Join(mountPoint, "config.txt")
	if _, err := os.Stat(configTxtPath); err == nil { // config.txt exists -> patch it
		if err = patchConfigTxt(mountPoint, availableOverlays, logger); err != nil {
			return fmt.Errorf("failed to patch config.txt: %w", err)
		}
	}
	return nil
}

func patchBootPartition(img *disk.Disk, bootPartition part.Partition, flavour imageFlavour, logger *slog.Logger) error {
	bootImg := path.Join(workDir, "boot.img")
	f, err := os.Create(bootImg)
	if err != nil {
		return errors.Join(common.ErrFailedToConfigureImage, err)
	}
	defer f.Close()

	n, err := bootPartition.ReadContents(img.Backend, f)
	if err != nil {
		return errors.Join(common.ErrFailedToConfigureImage, err)
	}
	if n != bootPartition.GetSize() {
		return errors.Join(common.ErrFailedToConfigureImage, fmt.Errorf("expected to read %d bytes from boot partition, but read %d", bootPartition.GetSize(), n))
	}
	f.Close()

	logger.Debug("Wrote boot partition to file", slog.String("path", bootImg), slog.Int64("bytes_written", n))
	bootMountPoint := path.Join(workDir, "boot")
	unmount, err := fusefat_mount(bootImg, bootMountPoint, logger)
	if err != nil {
		return err
	}
	_ = unmount
	defer unmount(true)

	if err = patchBootConfiguration(bootMountPoint, flavour, logger); err != nil {
		return errors.Join(common.ErrFailedToConfigureImage, err)
	}

	unmount(false)

	// write back to partition
	f, err = os.OpenFile(bootImg, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to open boot image for writing: %w", err)
	}
	defer f.Close()

	writable, err := img.Backend.Writable()
	if err != nil {
		return fmt.Errorf("failed to get writable backend: %w", err)
	}
	nnew, err := bootPartition.WriteContents(writable, f)
	if err != nil {
		return fmt.Errorf("failed to write back boot partition: %w", err)
	}
	if int64(nnew) != bootPartition.GetSize() {
		return fmt.Errorf("expected to write %d bytes to boot partition, but wrote %d", bootPartition.GetSize(), nnew)
	}

	return nil
}

func patchAppPartition(imgPath string, appPartition part.Partition, flavour imageFlavour, logger *slog.Logger) error {
	appfs := path.Join(workDir, "appfs")

	unmount, err := fuse2fs_mount(imgPath, appfs, int(appPartition.GetStart()), logger)
	if err != nil {
		return err
	}
	_ = unmount
	defer unmount(true)

	for src, dst := range AppInjectFiles {
		logger.Info("Injecting file into app partition", slog.String("src", src), slog.String("dst", dst))
		srcPath := src
		dstPath := path.Join(appfs, dst)

		dstDir := path.Dir(dstPath)
		if _, err = os.Stat(dstDir); err != nil {
			if err = os.MkdirAll(dstDir, 0755); err != nil {
				return fmt.Errorf("failed to create directory for %s: %w", dstPath, err)
			}
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return fmt.Errorf("failed to copy %s to %s: %w", srcPath, dstPath, err)
		}

		if err := os.Chown(dstPath, 1000, 1000); err != nil {
			return fmt.Errorf("failed to chown %s: %w", dstPath, err)
		}

		if err := os.Chmod(dstPath, 0555); err != nil {
			return fmt.Errorf("failed to chmod %s: %w", dstPath, err)
		}
	}

	// inject .image-flavour from IMAGE_ID env variable
	flavourFilePath := path.Join(appfs, ".image-flavour")
	if err := os.WriteFile(flavourFilePath, []byte(os.Getenv("IMAGE_ID")), 0444); err != nil {
		return fmt.Errorf("failed to write image flavour file %s: %w", flavourFilePath, err)
	}

	return nil
}

func patchDataPartition(imgPath string, dataPartition part.Partition, flavour imageFlavour, logger *slog.Logger) error {
	datafs := path.Join(workDir, "datafs")

	unmount, err := fuse2fs_mount(imgPath, datafs, int(dataPartition.GetStart()), logger)
	if err != nil {
		return err
	}
	_ = unmount
	defer unmount(true)

	// create data dir and set ownership to tezsign user
	dataMountPoint := path.Join(datafs, "tezsign")
	if _, err = os.Stat(dataMountPoint); err != nil {
		if err := os.MkdirAll(dataMountPoint, 0755); err != nil {
			return fmt.Errorf("failed to create data mount point %s: %w", dataMountPoint, err)
		}
	}
	if err := os.Chown(dataMountPoint, 1000, 1000); err != nil {
		return fmt.Errorf("failed to chown data mount point %s: %w", dataMountPoint, err)
	}

	return nil
}

func setupModules(rootFsPath, fileName string, modules []string, logger *slog.Logger) error {
	modulesLoadPath := path.Join(rootFsPath, "etc", "modules-load.d", fileName)
	return os.WriteFile(modulesLoadPath, []byte(strings.Join(modules, "\n")), 0644)
}

func patchRootPartition(imgPath string, rootPartition part.Partition, flavour imageFlavour, logger *slog.Logger) error {
	unmount, err := fuse2fs_mount(imgPath, path.Join(workDir, "rootfs"), int(rootPartition.GetStart()), logger)
	if err != nil {
		return err
	}
	_ = unmount
	defer unmount(true)

	// Patch /etc/fstab
	rootfs := path.Join(workDir, "rootfs")
	fstabPath := path.Join(rootfs, "etc", "fstab")

	err = PathFsTab(fstabPath, []mount{
		{point: "tmpfs /tmp", options: []string{"tmpfs", "defaults,noatime,nosuid,size=50m"}},
		{point: "tmpfs /var/log", options: []string{"tmpfs", "defaults,noatime,nosuid,size=50m"}},
		{point: "tmpfs /var/tmp", options: []string{"tmpfs", "defaults,noatime,nosuid,size=30m"}},
		{point: fmt.Sprintf("LABEL=%s /app", constants.AppPartitionLabel), options: []string{"ext4", "ro,exec,noatime,nofail,data=journal  0   2"}},
		{point: fmt.Sprintf("LABEL=%s /data", constants.DataPartitionLabel), options: []string{"ext4", "rw,noatime,nofail,data=journal   0   2"}},
	})

	bootMountPoint := path.Join(rootfs, "boot")
	if _, err := os.Stat(bootMountPoint); err == nil {
		if err = patchBootConfiguration(bootMountPoint, flavour, logger); err != nil {
			return errors.Join(common.ErrFailedToConfigureImage, err)
		}
	}

	// remove files
	for _, filePath := range ArmbianRootfsRemove {
		fullPath := path.Join(rootfs, filePath)
		if err := os.RemoveAll(fullPath); err != nil {
			return fmt.Errorf("failed to remove %s: %w", fullPath, err)
		}
	}

	for _, dirPath := range ArmbianRootFsCreateDirs {
		fullPath := path.Join(rootfs, dirPath)
		if err := os.MkdirAll(fullPath, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", fullPath, err)
		}
	}

	// inject files
	for src, dst := range ArmbianInjectFiles {
		srcPath := src
		dstPath := path.Join(rootfs, dst)

		if err := os.MkdirAll(path.Dir(dstPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", dstPath, err)
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return fmt.Errorf("failed to copy %s to %s: %w", srcPath, dstPath, err)
		}
	}

	// create symlinks
	for src, dst := range ArmbianCreateSymlinks {
		dstPath := path.Join(rootfs, dst)

		if err := os.MkdirAll(path.Dir(dstPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory for symlink %s: %w", dstPath, err)
		}
		if err := os.Symlink(src, dstPath); err != nil {
			return fmt.Errorf("failed to create symlink from %s to %s: %w", src, dstPath, err)
		}
	}

	// adjust permissions
	for filePath, mode := range ArmbianAdjustPermissions {
		fullPath := path.Join(rootfs, filePath)
		if err := os.Chmod(fullPath, mode); err != nil {
			return fmt.Errorf("failed to chmod %o %s: %w", mode, fullPath, err)
		}
	}

	switch flavour {
	case DevImage:
		for _, filePath := range DevArmbianRootfsRemove {
			fullPath := path.Join(rootfs, filePath)
			if err := os.RemoveAll(fullPath); err != nil {
				return fmt.Errorf("failed to remove %s: %w", fullPath, err)
			}
		}

		for src, dst := range DevArmbianInjectFiles {
			srcPath := src
			dstPath := path.Join(rootfs, dst)

			if err := os.MkdirAll(path.Dir(dstPath), 0755); err != nil {
				return fmt.Errorf("failed to create directory for %s: %w", dstPath, err)
			}
			if err := copyFile(srcPath, dstPath); err != nil {
				return fmt.Errorf("failed to copy %s to %s: %w", srcPath, dstPath, err)
			}
		}

		for src, dst := range DevArmbianCreateSymlinks {
			dstPath := path.Join(rootfs, dst)

			if err := os.MkdirAll(path.Dir(dstPath), 0755); err != nil {
				return fmt.Errorf("failed to create directory for symlink %s: %w", dstPath, err)
			}

			if err := os.Symlink(src, dstPath); err != nil {
				return fmt.Errorf("failed to create symlink from %s to %s: %w", src, dstPath, err)
			}
		}

		for filePath, mode := range DevArmbianAdjustPermissions {
			fullPath := path.Join(rootfs, filePath)
			if err := os.Chmod(fullPath, mode); err != nil {
				return fmt.Errorf("failed to chmod %o %s: %w", mode, fullPath, err)
			}
		}
	default:
		// no dev files to inject
	}

	if err = setupModules(rootfs, "tezsign-usb.conf", PreloadTezsignUsbModules, logger); err != nil {
		return fmt.Errorf("failed to setup tezsign-usb modules: %w", err)
	}

	return nil
}

func ConfigureImage(workDir, imagePath string, flavour imageFlavour, logger *slog.Logger) error {
	img, err := diskfs.Open(imagePath, diskfs.WithOpenMode(diskfs.ReadWrite))
	if err != nil {
		return errors.Join(common.ErrFailedToOpenImage, err)
	}

	bootPartition, rootfsPartition, appPartition, dataPartition, err := common.GetTezsignPartitions(img)
	if err != nil {
		return errors.Join(common.ErrFailedToConfigureImage, err)
	}

	logger.Info("Found partitions",
		// slog.Group("boot", slog.Int64("start", bootPartition.GetStart()), slog.Int64("size", bootPartition.GetSize())),
		slog.Group("rootfs", slog.Int64("start", rootfsPartition.GetStart()), slog.Int64("size", rootfsPartition.GetSize())),
		slog.Group("app", slog.Int64("start", appPartition.GetStart()), slog.Int64("size", appPartition.GetSize())),
		slog.Group("data", slog.Int64("start", dataPartition.GetStart()), slog.Int64("size", dataPartition.GetSize())))

	// patch boot partition
	if bootPartition != nil { // some images may not have a separate boot partition
		if err := patchBootPartition(img, bootPartition, flavour, logger); err != nil {
			return errors.Join(common.ErrFailedToConfigureImage, err)
		}
	} else {
		logger.Info("No separate boot partition found, skipping boot partition patching.")
	}

	// patch rootfs partition
	if err := patchRootPartition(imagePath, rootfsPartition, flavour, logger); err != nil {
		return errors.Join(common.ErrFailedToConfigureImage, err)
	}

	if err := patchAppPartition(imagePath, appPartition, flavour, logger); err != nil {
		return errors.Join(common.ErrFailedToConfigureImage, err)
	}

	if err := patchDataPartition(imagePath, dataPartition, flavour, logger); err != nil {
		return errors.Join(common.ErrFailedToConfigureImage, err)
	}

	logger.Info("✅ Successfully added configured the image.")

	return nil
}
