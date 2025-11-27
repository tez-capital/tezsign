package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/partition/gpt"
	"github.com/diskfs/go-diskfs/partition/mbr"
	"github.com/diskfs/go-diskfs/partition/part"
	"github.com/tez-capital/tezsign/tools/common"
	"github.com/tez-capital/tezsign/tools/constants"
	"github.com/ulikunitz/xz"
)

type UpdateKind string

const (
	UpdateKindFull    UpdateKind = "full"
	UpdateKindAppOnly UpdateKind = "app"
)

type selectionStage int

const (
	stageDevice selectionStage = iota
	stageKind
)

type deviceCandidate struct {
	Name      string
	Path      string
	SizeBytes uint64
	Model     string
	Status    string
	Valid     bool
}

type selectionModel struct {
	stage          selectionStage
	devices        []deviceCandidate
	table          table.Model
	kindCursor     int
	selectedDevice *deviceCandidate
	selectedKind   UpdateKind
	err            error
}

type decompressModel struct {
	source string
	target string
	total  int64
	reader *countingReader
	cancel func()
	err    error
	done   bool
}

type tickMsg time.Time

type finishMsg struct {
	err error
}

func newSelectionModel(devices []deviceCandidate) selectionModel {
	columns := []table.Column{
		{Title: "Name", Width: 10},
		{Title: "Size", Width: 12},
		{Title: "Status", Width: 66},
		{Title: "Model", Width: 18},
		{Title: "Path", Width: 20},
	}

	rows := make([]table.Row, 0, len(devices))
	for _, dev := range devices {
		rows = append(rows, table.Row{
			dev.Name,
			byteCountToHumanReadable(int64(dev.SizeBytes)),
			dev.Status,
			dev.Model,
			dev.Path,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(10),
	)
	t.SetStyles(newTableStyles())

	return selectionModel{
		stage:        stageDevice,
		devices:      devices,
		table:        t,
		selectedKind: UpdateKindFull,
	}
}

func newTableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	return s
}

func (m selectionModel) Init() tea.Cmd {
	return nil
}

func newDecompressModel(source, target string, total int64, reader *countingReader, cancel func()) decompressModel {
	return decompressModel{
		source: source,
		target: target,
		total:  total,
		reader: reader,
		cancel: cancel,
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m decompressModel) Init() tea.Cmd {
	return tickCmd()
}

func (m decompressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if m.done {
			return m, nil
		}
		return m, tickCmd()
	case finishMsg:
		m.done = true
		m.err = msg.err
		return m, tea.Quit
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			if !m.done && m.cancel != nil {
				m.cancel()
			}
			m.done = true
			m.err = errors.New("decompression cancelled")
			return m, tea.Quit
		}
	}
	return m, nil
}

func renderProgressBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	fill := int((pct / 100) * float64(width))
	if fill > width {
		fill = width
	}
	return fmt.Sprintf("[%s%s] %5.1f%%", strings.Repeat("█", fill), strings.Repeat("░", width-fill), pct)
}

func (m decompressModel) View() string {
	read := m.reader.BytesRead()
	var pct float64 = -1
	if m.total > 0 {
		pct = float64(read) / float64(m.total) * 100
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("%s → %s\n\n", m.source, m.target))
	if pct >= 0 {
		builder.WriteString(renderProgressBar(pct, 40))
		builder.WriteString(fmt.Sprintf("  %s / %s\n", byteCountToHumanReadable(read), byteCountToHumanReadable(m.total)))
	} else {
		builder.WriteString(fmt.Sprintf("%s read\n", byteCountToHumanReadable(read)))
	}

	if m.done {
		if m.err != nil {
			builder.WriteString(fmt.Sprintf("\nError: %v\n", m.err))
		} else {
			builder.WriteString("\nDone.\n")
		}
	} else {
		builder.WriteString("\nPress q to cancel.")
	}
	return builder.String()
}

func (m selectionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.err = errors.New("update cancelled")
			return m, tea.Quit
		}

		switch m.stage {
		case stageDevice:
			switch msg.String() {
			case "up", "k":
				m.table.MoveUp(1)
			case "down", "j":
				m.table.MoveDown(1)
			case "enter":
				if len(m.devices) == 0 {
					m.err = errors.New("no TezSign SD cards detected")
					return m, tea.Quit
				}
				cursor := m.table.Cursor()
				if cursor < 0 || cursor >= len(m.devices) {
					m.err = errors.New("invalid selection")
					return m, tea.Quit
				}
				m.selectedDevice = &m.devices[cursor]
				m.stage = stageKind
			}
		case stageKind:
			switch msg.String() {
			case "up", "k":
				if m.kindCursor > 0 {
					m.kindCursor--
				}
			case "down", "j":
				if m.kindCursor < 1 {
					m.kindCursor++
				}
			case "enter":
				options := []UpdateKind{UpdateKindFull, UpdateKindAppOnly}
				m.selectedKind = options[m.kindCursor]
				return m, tea.Quit
			}
		}
	case tea.WindowSizeMsg:
		width := msg.Width - 4
		if width < 20 {
			width = 20
		}
		m.table.SetWidth(width)
	}

	return m, nil
}

func (m selectionModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	switch m.stage {
	case stageDevice:
		if len(m.devices) == 0 {
			return "No TezSign SD cards detected. Press q to exit."
		}
		return fmt.Sprintf(
			"Select a device (↑/↓ to navigate, enter to choose)\n\n%s\n\nPress enter to continue or q to cancel.",
			m.table.View(),
		)
	case stageKind:
		options := []struct {
			kind  UpdateKind
			title string
		}{
			{UpdateKindFull, "Full (boot, rootfs and app)"},
			{UpdateKindAppOnly, "TezSign app only"},
		}

		var sb strings.Builder
		sb.WriteString("Select update kind (↑/↓ to navigate, enter to start)\n\n")
		for i, option := range options {
			cursor := "  "
			if i == m.kindCursor {
				cursor = "> "
			}
			sb.WriteString(fmt.Sprintf("%s%s\n", cursor, option.title))
		}
		sb.WriteString("\nPress q to cancel.")
		return sb.String()
	default:
		return ""
	}
}

func runSelection(devices []deviceCandidate) (deviceCandidate, UpdateKind, error) {
	program := tea.NewProgram(newSelectionModel(devices))
	model, err := program.Run()
	if err != nil {
		return deviceCandidate{}, "", err
	}

	selection, ok := model.(selectionModel)
	if !ok {
		return deviceCandidate{}, "", errors.New("failed to read selection state")
	}

	if selection.err != nil {
		return deviceCandidate{}, "", selection.err
	}

	if selection.selectedDevice == nil {
		return deviceCandidate{}, "", errors.New("no device selected")
	}

	return *selection.selectedDevice, selection.selectedKind, nil
}

func discoverTezsignDevices(logger *slog.Logger) ([]deviceCandidate, error) {
	paths, err := filepath.Glob("/sys/block/*/removable")
	if err != nil {
		return nil, fmt.Errorf("failed to list block devices: %w", err)
	}

	var devices []deviceCandidate
	for _, removablePath := range paths {
		flag, err := os.ReadFile(removablePath)
		if err != nil || strings.TrimSpace(string(flag)) != "1" {
			continue
		}

		name := filepath.Base(filepath.Dir(removablePath))
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
			continue
		}

		devicePath := filepath.Join("/dev", name)
		sizeBytes := readBlockSizeBytes(name)
		model := strings.TrimSpace(readSysfsValue(filepath.Join("/sys/block", name, "device/model")))

		isTezsign, status := probeTezsignDevice(devicePath)
		if !isTezsign {
			logger.Debug("Device did not validate as TezSign", "device", devicePath, "status", status)
		}

		devices = append(devices, deviceCandidate{
			Name:      name,
			Path:      devicePath,
			SizeBytes: sizeBytes,
			Model:     model,
			Status:    status,
			Valid:     isTezsign,
		})
	}

	if len(devices) == 0 {
		return nil, errors.New("no removable block devices detected")
	}

	return devices, nil
}

func readSysfsValue(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func readBlockSizeBytes(name string) uint64 {
	sizeContent := readSysfsValue(filepath.Join("/sys/block", name, "size"))
	sectors, err := strconv.ParseUint(strings.TrimSpace(sizeContent), 10, 64)
	if err != nil {
		return 0
	}
	return sectors * 512 // sectors are 512-byte blocks
}

func probeTezsignDevice(path string) (bool, string) {
	disk, _, _, appPartition, err := loadImage(path, diskfs.ReadOnly)
	if err != nil {
		return false, err.Error()
	}
	defer disk.Close()

	ok, err := checkTezsignMarker(disk, appPartition)
	switch {
	case err != nil:
		return true, "marker check skipped"
	case !ok:
		return true, "marker /tezsign missing (will be updated)"
	default:
		return true, "OK"
	}
}

func checkTezsignMarker(disk *disk.Disk, appPartition part.Partition) (bool, error) {
	table, err := disk.GetPartitionTable()
	if err != nil {
		return false, err
	}

	switch t := table.(type) {
	case *gpt.Table:
		if len(t.Partitions) < 3 {
			return false, nil
		}
		hasApp := false
		hasData := false
		for _, p := range t.Partitions {
			name := strings.TrimSpace(p.Name)
			if name == constants.AppPartitionLabel {
				hasApp = true
			}
			if name == constants.DataPartitionLabel {
				hasData = true
			}
		}
		return hasApp && hasData, nil
	case *mbr.Table:
		// For MBR (Armbian Pi images), expect four partitions (boot/root/app/data).
		return len(t.Partitions) >= 4, nil
	default:
		return false, nil
	}
}

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

type countingReader struct {
	r    io.Reader
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	atomic.AddInt64(&c.read, int64(n))
	return n, err
}

func (c *countingReader) BytesRead() int64 {
	return atomic.LoadInt64(&c.read)
}

func maybeDecompressSource(path string, logger *slog.Logger) (string, func(), error) {
	if !strings.HasSuffix(path, ".xz") {
		return path, func() {}, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open compressed source %s: %w", path, err)
	}
	stat, _ := f.Stat()
	totalBytes := stat.Size()

	cr := &countingReader{r: f}
	r, err := xz.NewReader(cr)
	if err != nil {
		f.Close()
		return "", nil, fmt.Errorf("failed to create xz reader: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "tezsign_img_*.img")
	if err != nil {
		f.Close()
		return "", nil, fmt.Errorf("failed to create temp file for decompression: %w", err)
	}

	logger.Info("Decompressing source image", "source", path, "destination", tmpFile.Name())

	cancel := func() {
		f.Close()
		tmpFile.Close()
	}

	p := tea.NewProgram(newDecompressModel(fmt.Sprintf("Decompress %s", filepath.Base(path)), tmpFile.Name(), totalBytes, cr, cancel))

	go func() {
		_, copyErr := io.Copy(tmpFile, r)
		tmpFile.Close()
		f.Close()
		p.Send(finishMsg{err: copyErr})
	}()

	model, progErr := p.Run()
	if progErr != nil {
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("failed to render decompress progress: %w", progErr)
	}

	res, ok := model.(decompressModel)
	if !ok {
		os.Remove(tmpFile.Name())
		return "", nil, errors.New("unexpected model type after decompression")
	}

	if res.err != nil {
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("failed to decompress source image: %w", res.err)
	}

	cleanup := func() {
		os.Remove(tmpFile.Name())
	}

	return tmpFile.Name(), cleanup, nil
}

func detectDeviceFlavor(devicePath string) (string, error) {
	f, err := os.Open(devicePath)
	if err != nil {
		return "", fmt.Errorf("failed to open device %s: %w", devicePath, err)
	}
	defer f.Close()

	d, err := diskfs.OpenBackend(file.New(f, false), diskfs.WithOpenMode(diskfs.ReadOnly), diskfs.WithSectorSize(diskfs.SectorSizeDefault))
	if err != nil {
		return "", fmt.Errorf("failed to open disk backend for %s: %w", devicePath, err)
	}
	defer d.Close()

	table, err := d.GetPartitionTable()
	if err != nil {
		return "", fmt.Errorf("failed to read partition table for %s: %w", devicePath, err)
	}

	switch table.(type) {
	case *gpt.Table:
		return "radxa_zero3.img.xz", nil
	case *mbr.Table:
		return "raspberry_pi.img.xz", nil
	default:
		return "", errors.New("unknown partition table type")
	}
}

func downloadWithProgress(url string, logger *slog.Logger) (string, func(), error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", nil, fmt.Errorf("failed to download image: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return "", nil, fmt.Errorf("failed to download image: %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp("", "tezsign_download_*.img.xz")
	if err != nil {
		resp.Body.Close()
		return "", nil, fmt.Errorf("failed to create temp file for download: %w", err)
	}

	total := resp.ContentLength
	cr := &countingReader{r: resp.Body}
	cancel := func() {
		resp.Body.Close()
		tmpFile.Close()
		os.Remove(tmpFile.Name())
	}

	p := tea.NewProgram(newDecompressModel(fmt.Sprintf("Download %s", filepath.Base(url)), tmpFile.Name(), total, cr, cancel))

	go func() {
		_, copyErr := io.Copy(tmpFile, cr)
		tmpFile.Close()
		resp.Body.Close()
		p.Send(finishMsg{err: copyErr})
	}()

	model, progErr := p.Run()
	if progErr != nil {
		cancel()
		return "", nil, fmt.Errorf("failed to render download progress: %w", progErr)
	}

	res, ok := model.(decompressModel)
	if !ok {
		cancel()
		return "", nil, errors.New("unexpected model type after download")
	}

	if res.err != nil {
		cancel()
		return "", nil, fmt.Errorf("failed to download image: %w", res.err)
	}

	cleanup := func() {
		os.Remove(tmpFile.Name())
	}

	return tmpFile.Name(), cleanup, nil
}

func copyPartitionData(srcDisk *disk.Disk, srcPartition part.Partition, dstDisk *disk.Disk, dstPartition part.Partition, logger *slog.Logger) error {
	pr, pw := io.Pipe()
	writableDst, err := dstDisk.Backend.Writable()
	if err != nil {
		return errors.New("failed to get writable backend for destination disk")
	}

	progressLogger := NewProgressLogger(pw, logger)

	var wg sync.WaitGroup
	var readErr, writeErr error
	var readBytes int64

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer pw.Close() // Close the writer side of the pipe when done

		// ReadContents(backend, out io.Writer) streams data FROM the partition TO the provided writer (pw).
		readBytes, readErr = srcPartition.ReadContents(srcDisk.Backend, progressLogger)
		if readErr != nil {
			logger.Error("Failed to read contents from source partition", "error", readErr)
			return
		}
	}()

	writtenBytes, writeErr := dstPartition.WriteContents(writableDst, pr)
	if writeErr != nil {
		logger.Error("Failed to write contents to destination partition", "error", writeErr)
	}
	pr.Close()
	wg.Wait()

	if readErr != nil {
		return errors.New("error occurred while reading from source partition: " + readErr.Error())
	}
	if writeErr != nil {
		return errors.New("error occurred while writing to destination partition: " + writeErr.Error())
	}
	if uint64(readBytes) != writtenBytes {
		return errors.New("mismatch in bytes read and written")
	}
	return nil
}

func performUpdate(source, destination string, kind UpdateKind, logger *slog.Logger) error {
	logger.Info("Starting TezSign updater", "source", source, "destination", destination, "kind", string(kind))

	sourcePath, cleanup, err := maybeDecompressSource(source, logger)
	if err != nil {
		return err
	}
	defer cleanup()

	sourceImg, sourceBootPartition, sourceRootfsPartition, sourceAppPartition, err := loadImage(sourcePath, diskfs.ReadOnly)
	if err != nil {
		return fmt.Errorf("failed to load source image: %w", err)
	}
	defer sourceImg.Close()

	dstImg, destinationBootPartition, destinationRootfsPartition, destinationAppPartition, err := loadImage(destination, diskfs.ReadWriteExclusive)
	if err != nil {
		return fmt.Errorf("failed to load destination image: %w", err)
	}
	defer dstImg.Close()

	if ok, err := checkTezsignMarker(dstImg, destinationAppPartition); err != nil {
		logger.Debug("Skipping marker check", "error", err)
	} else if !ok {
		logger.Debug("Destination missing /tezsign marker; proceeding and will overwrite app partition")
	}

	switch kind {
	case UpdateKindFull:
		if (sourceBootPartition == nil || destinationBootPartition == nil) && (sourceBootPartition != destinationBootPartition) {
			return errors.New("boot partition missing in source image or destination device, cannot proceed with full update")
		}
		if sourceBootPartition != nil && sourceBootPartition.GetSize() != destinationBootPartition.GetSize() {
			return errors.New("boot partition size mismatch between source image and destination device, cannot proceed with update")
		}

		if sourceRootfsPartition.GetSize() != destinationRootfsPartition.GetSize() {
			return errors.New("rootfs partition size mismatch between source image and destination device, cannot proceed with update")
		}

		if sourceAppPartition.GetSize() != destinationAppPartition.GetSize() {
			return errors.New("app partition size mismatch between source image and destination device, cannot proceed with update")
		}

		if sourceBootPartition != nil {
			logger.Info("Updating boot partition...")
			if err = copyPartitionData(sourceImg, sourceBootPartition, dstImg, destinationBootPartition, logger); err != nil {
				return fmt.Errorf("failed to update boot partition: %w", err)
			}
		}

		logger.Info("Updating rootfs partition...")
		if err = copyPartitionData(sourceImg, sourceRootfsPartition, dstImg, destinationRootfsPartition, logger); err != nil {
			return fmt.Errorf("failed to update rootfs partition: %w", err)
		}

		logger.Info("Updating app partition...")
		if err = copyPartitionData(sourceImg, sourceAppPartition, dstImg, destinationAppPartition, logger); err != nil {
			return fmt.Errorf("failed to update app partition: %w", err)
		}
	case UpdateKindAppOnly:
		logger.Info("Updating app partition...")
		if err = copyPartitionData(sourceImg, sourceAppPartition, dstImg, destinationAppPartition, logger); err != nil {
			return fmt.Errorf("failed to update app partition: %w", err)
		}
	default:
		return fmt.Errorf("unsupported update kind: %s", kind)
	}

	return nil
}

func main() {
	logger := slog.Default()

	var source string
	var sourceProvided bool
	if len(os.Args) >= 2 {
		source = os.Args[1]
		sourceProvided = true
	}

	// Keep the previous non-interactive flow when destination is provided explicitly.
	if sourceProvided && len(os.Args) >= 3 {
		destination := os.Args[2]
		kind := UpdateKindFull
		if len(os.Args) >= 4 {
			kind = UpdateKind(os.Args[3])
			switch kind {
			case UpdateKindFull, UpdateKindAppOnly:
			default:
				logger.Error("Invalid update kind. Valid options are: full, app")
				os.Exit(1)
			}
		}

		if err := performUpdate(source, destination, kind, logger); err != nil {
			logger.Error("Update failed", "error", err)
			os.Exit(1)
		}

		logger.Info("Update completed successfully")
		return
	}

	devices, err := discoverTezsignDevices(logger)
	if err != nil {
		logger.Error("Failed to discover TezSign devices", "error", err)
		os.Exit(1)
	}

	selectedDevice, kind, err := runSelection(devices)
	if err != nil {
		logger.Error("Selection failed", "error", err)
		os.Exit(1)
	}

	if !sourceProvided {
		imageName, err := detectDeviceFlavor(selectedDevice.Path)
		if err != nil {
			logger.Error("Failed to detect device flavor", "error", err)
			os.Exit(1)
		}
		url := fmt.Sprintf("%s%s", constants.LatestReleaseURL, imageName)
		downloaded, cleanup, err := downloadWithProgress(url, logger)
		if err != nil {
			logger.Error("Failed to download image", "error", err)
			os.Exit(1)
		}
		defer cleanup()
		source = downloaded
	}

	if _, err := os.Stat(source); err != nil {
		logger.Error("Invalid source image", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Updating %s with a %s update...\n\n", selectedDevice.Path, string(kind))

	if err := performUpdate(source, selectedDevice.Path, kind, logger); err != nil {
		logger.Error("Update failed", "error", err)
		os.Exit(1)
	}

	fmt.Println("✅ Update completed successfully")
}
