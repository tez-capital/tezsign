package main

import (
	"os"
)

type imageFlavour string

const (
	StandardImage imageFlavour = "prod"
	DevImage      imageFlavour = "dev"
)

const (
	appPartitionSizeMB  = 64
	dataPartitionSizeMB = 128

	workDir  = "/tmp/tezsign_image_builder"
	tmpImage = workDir + "/image.img"

	DISABLE_UNMOUNTS = false // set to true to disable unmounts for debugging
)

var (
	// alignPartitionsTo = uint64(3 * 1024 * 1024 * 1024) // Default to 2GB alignment

	PreloadTezsignUsbModules = []string{
		"configfs",
		"libcomposite",
		"usb_f_fs",
		"usb_f_ecm",
	}

	ArmbianRootfsRemove = []string{
		"/root/.not_logged_in_yet",
		"/usr/lib/armbian/armbian-firstlogin",
		"/etc/profile.d/armbian-check-first-login.sh",
		"/etc/systemd/system/getty@.service.d",        // remove custom getty settings - mainly auto-login
		"/etc/systemd/system/serial-getty@.service.d", // remove custom getty settings - mainly auto-login
		"/usr/lib/firmware/qcom",                      // qcom firmware
	}

	ArmbianRootFsCreateDirs = []string{
		"/app",
		"/data",
	}

	ArmbianInjectFiles = map[string]string{
		"tools/builder/assets/first-boot-setup.sh":            "/usr/local/bin/first-boot-setup.sh",
		"tools/builder/assets/first-boot-setup.service":       "/etc/systemd/system/first-boot-setup.service",
		"tools/builder/assets/setup-gadget.sh":                "/usr/local/bin/setup-gadget.sh",
		"tools/builder/assets/setup-gadget.service":           "/etc/systemd/system/setup-gadget.service",
		"tools/builder/assets/attach-gadget.sh":               "/usr/local/bin/attach-gadget.sh",
		"tools/builder/assets/attach-gadget.service":          "/etc/systemd/system/attach-gadget.service",
		"tools/builder/assets/ffs_registrar":                  "/usr/local/bin/ffs_registrar",
		"tools/builder/assets/ffs_registrar.service":          "/etc/systemd/system/ffs_registrar.service",
		"tools/builder/assets/tezsign.service":                "/etc/systemd/system/tezsign.service",
		"tools/builder/assets/generate-serial-number.sh":      "/usr/local/bin/generate-serial-number.sh",
		"tools/builder/assets/setup-gadget-dev-dummy.service": "/etc/systemd/system/setup-gadget-dev.service", // dummy to satisfy dependencies
	}

	AppInjectFiles = map[string]string{
		"tools/builder/assets/tezsign": "/tezsign",
	}

	ArmbianAdjustPermissions = map[string]os.FileMode{
		"/usr/local/bin/first-boot-setup.sh":       0700, // Only root can execute
		"/usr/local/bin/setup-gadget.sh":           0700,
		"/usr/local/bin/attach-gadget.sh":          0700,
		"/usr/local/bin/ffs_registrar":             0700,
		"/usr/local/bin/generate-serial-number.sh": 0700,
	}

	ArmbianCreateSymlinks = map[string]string{
		"/etc/systemd/system/first-boot-setup.service": "/etc/systemd/system/multi-user.target.wants/first-boot-setup.service",
		"/etc/systemd/system/setup-gadget.service":     "/etc/systemd/system/multi-user.target.wants/setup-gadget.service",
		"/etc/systemd/system/attach-gadget.service":    "/etc/systemd/system/multi-user.target.wants/attach-gadget.service",
		"/etc/systemd/system/ffs_registrar.service":    "/etc/systemd/system/multi-user.target.wants/ffs_registrar.service",
		"/etc/systemd/system/tezsign.service":          "/etc/systemd/system/multi-user.target.wants/tezsign.service",
	}

	ArmbianActivateOverlays = map[string]string{
		"radxa-zero3-disabled-ethernet": "",
		"radxa-zero3-disabled-wireless": "",
		"rk3568-dwc3-peripheral":        "",
		"dwc2":                          "dr_mode=otg",
		"disable-bt":                    "",
		"disable-wifi":                  "",
	}

	DevArmbianInjectFiles = map[string]string{
		"tools/builder/assets/setup-gadget-dev.sh":       "/usr/local/bin/setup-gadget-dev.sh",
		"tools/builder/assets/setup-gadget-dev.service":  "/etc/systemd/system/setup-gadget-dev.service",
		"tools/builder/assets/attach-gadget-dev.sh":      "/usr/local/bin/attach-gadget-dev.sh",
		"tools/builder/assets/attach-gadget-dev.service": "/etc/systemd/system/attach-gadget-dev.service",
		"tools/builder/assets/enable-dev.sh":             "/usr/local/bin/enable-dev.sh",
	}

	DevArmbianRootfsRemove = []string{}

	DevArmbianAdjustPermissions = map[string]os.FileMode{
		"/usr/local/bin/setup-gadget-dev.sh":  0700, // Only root can execute
		"/usr/local/bin/attach-gadget-dev.sh": 0700, // Only root can execute
		"/usr/local/bin/enable-dev.sh":        0700, // Only root can execute
	}

	DevArmbianCreateSymlinks = map[string]string{
		"/etc/systemd/system/setup-gadget-dev.service":  "/etc/systemd/system/multi-user.target.wants/setup-gadget-dev.service",
		"/etc/systemd/system/attach-gadget-dev.service": "/etc/systemd/system/multi-user.target.wants/attach-gadget-dev.service",
	}
)
