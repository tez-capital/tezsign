package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
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
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/partition"
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
				if !m.devices[cursor].Valid {
					m.err = errors.New("selected device is not a valid TezSign SD card")
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
	disk, _, _, _, err := loadImage(path, diskfs.ReadOnly)
	if err != nil {
		return false, err.Error()
	}
	defer disk.Close()

	ok, err := checkTezsignMarker(disk)
	switch {
	case err != nil:
		return false, "marker check failed"
	case !ok:
		return false, "device does not match TezSign layout"
	default:
		return true, "OK"
	}
}

func checkTezsignMarker(disk *disk.Disk) (bool, error) {
	table, err := disk.GetPartitionTable()
	if err != nil {
		return false, err
	}
	if !hasExpectedPartitionCount(table) {
		return false, nil
	}
	hasApp := false
	hasData := false
	for idx := range table.GetPartitions() {
		fs, err := disk.GetFilesystem(idx + 1)
		if err == nil {
			label := strings.TrimSpace(fs.Label())
			if label == constants.AppPartitionLabel {
				if _, err := fs.OpenFile("/tezsign", os.O_RDONLY); err == nil {
					hasApp = true
				}
			}
			if label == constants.DataPartitionLabel {
				hasData = true
			}
		}
	}
	return hasApp && hasData, nil
}

func hasExpectedPartitionCount(t partition.Table) bool {
	switch tt := t.(type) {
	case *gpt.Table:
		return len(tt.Partitions) >= 3
	case *mbr.Table:
		nonZero := 0
		for _, p := range tt.Partitions {
			if p != nil && p.Size > 0 {
				nonZero++
			}
		}
		return nonZero == 4
	default:
		return false
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

func downloadWithProgress(url string) (string, func(), error) {
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

var validFlavours = map[string]bool{
	"raspberry_pi":     true,
	"raspberry_pi.dev": true,
	"radxa_zero3":      true,
	"radxa_zero3.dev":  true,
}

func deviceFlavour(devicePath string) (string, error) {
	d, _, _, appPartition, err := loadImage(devicePath, diskfs.ReadOnly)
	if err != nil {
		return "", err
	}
	defer d.Close()

	fs, err := filesystemForPartition(d, appPartition)
	if err != nil {
		return "", err
	}

	flavour, err := readImageFlavour(fs)
	if err != nil {
		return "", err
	}
	if flavour != "" {
		return flavour, nil
	}

	tbl, err := d.GetPartitionTable()
	if err != nil {
		return "", err
	}

	fallback := flavourFromTable(tbl)
	if fallback == "" {
		return "", errors.New("unable to determine image flavour")
	}
	return fallback, nil
}

func flavourFromTable(t partition.Table) string {
	switch t.(type) {
	case *gpt.Table:
		return "radxa_zero3"
	case *mbr.Table:
		return "raspberry_pi"
	default:
		return ""
	}
}

func readImageFlavour(fs filesystem.FileSystem) (string, error) {
	f, err := fs.OpenFile("/.image-flavour", os.O_RDONLY)
	if err != nil {
		// Some filesystems return a custom error string rather than os.ErrNotExist; treat any failure as "missing".
		return "", nil
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	flavour := strings.TrimSpace(string(data))
	if !validFlavours[flavour] {
		return "", nil
	}
	return flavour, nil
}

func ensureImageFlavour(fs filesystem.FileSystem, fallback string, logger *slog.Logger) (string, error) {
	flavour, err := readImageFlavour(fs)
	if err != nil {
		return "", err
	}
	if flavour != "" {
		return flavour, nil
	}
	if fallback == "" {
		return "", errors.New("unable to determine image flavour")
	}

	if tmp, err := fs.OpenFile("/.image-flavour", os.O_WRONLY|os.O_CREATE|os.O_TRUNC); err == nil {
		if _, err := tmp.Write([]byte(fallback)); err != nil {
			logger.Debug("Failed to write /.image-flavour; continuing", "error", err)
		}
		tmp.Close()
		_ = fs.Chmod("/.image-flavour", 0444)
	} else {
		logger.Debug("Failed to persist /.image-flavour; continuing", "error", err)
	}
	return fallback, nil
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

	dstImg, destinationBootPartition, destinationRootfsPartition, destinationAppPartition, err := loadImage(destination, diskfs.ReadWriteExclusive)
	if err != nil {
		return fmt.Errorf("failed to load destination image: %w", err)
	}
	defer dstImg.Close()

	if ok, err := checkTezsignMarker(dstImg); err != nil {
		logger.Debug("Skipping marker check", "error", err)
	} else if !ok {
		logger.Debug("Destination missing /tezsign marker; proceeding and will overwrite app partition")
	}

	switch kind {
	case UpdateKindFull:
		sourceImg, sourceBootPartition, sourceRootfsPartition, sourceAppPartition, err := loadImage(sourcePath, diskfs.ReadOnly)
		if err != nil {
			return fmt.Errorf("failed to load source image: %w", err)
		}
		defer sourceImg.Close()

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
		return errors.New("app-only updates require a gadget binary, not an image")
	default:
		return fmt.Errorf("unsupported update kind: %s", kind)
	}

	return nil
}

func performAppBinaryUpdate(binaryPath, destination string, logger *slog.Logger) error {
	logger.Info("Starting TezSign app-only update", "source", binaryPath, "destination", destination)

	dstImg, _, _, destinationAppPartition, err := loadImage(destination, diskfs.ReadWriteExclusive)
	if err != nil {
		return fmt.Errorf("failed to load destination image: %w", err)
	}
	defer dstImg.Close()

	if ok, err := checkTezsignMarker(dstImg); err != nil {
		return fmt.Errorf("marker check failed: %w", err)
	} else if !ok {
		return errors.New("destination does not match TezSign layout; aborting")
	}

	fs, err := filesystemForPartition(dstImg, destinationAppPartition)
	if err != nil {
		return fmt.Errorf("failed to open app filesystem: %w", err)
	}

	table, err := dstImg.GetPartitionTable()
	if err != nil {
		return fmt.Errorf("failed to read partition table: %w", err)
	}

	currentFlavour, _ := readImageFlavour(fs)
	fallback := flavourFromTable(table)
	if currentFlavour != "" {
		fallback = currentFlavour
	}
	flavour, err := ensureImageFlavour(fs, fallback, logger)
	if err != nil {
		return fmt.Errorf("failed to ensure image flavour: %w", err)
	}
	logger.Info("Using image flavour", "flavour", flavour)

	in, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to open gadget binary: %w", err)
	}
	defer in.Close()

	writeBinary := func() error {
		out, err := fs.OpenFile("/tezsign", os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			return fmt.Errorf("failed to open /tezsign on destination: %w", err)
		}

		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return fmt.Errorf("failed to write gadget binary: %w", err)
		}
		out.Close()

		// Best-effort permission set; ignore failures on read-only fs.
		_ = fs.Chmod("/tezsign", 0755)

		// Verify we can read back the binary (basic assurance write succeeded).
		verify, err := fs.OpenFile("/tezsign", os.O_RDONLY)
		if err != nil {
			return fmt.Errorf("failed to verify /tezsign after write: %w", err)
		}
		if _, err := io.Copy(io.Discard, verify); err != nil {
			verify.Close()
			return fmt.Errorf("failed to read back /tezsign after write: %w", err)
		}
		verify.Close()
		return nil
	}

	if err := writeBinary(); err != nil {
		logger.Warn("Direct write via go-diskfs failed, retrying via mount", "error", err)
		if err := writeAppViaMount(binaryPath, flavour, logger); err != nil {
			return fmt.Errorf("failed to write gadget binary (fallback mount): %w", err)
		}
	}

	return nil
}

func writeAppViaMount(binaryPath, flavour string, logger *slog.Logger) error {
	appDev, err := filepath.EvalSymlinks("/dev/disk/by-label/app")
	if err != nil {
		return fmt.Errorf("failed to resolve /dev/disk/by-label/app: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "tezsign_app_mount_")
	if err != nil {
		return fmt.Errorf("failed to create temp mount dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	mountCmd := exec.Command("mount", "-o", "rw", appDev, tmpDir)
	if out, err := mountCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to mount app partition (%s): %v: %s", appDev, err, string(out))
	}
	defer exec.Command("umount", tmpDir).Run()

	dstPath := filepath.Join(tmpDir, "tezsign")
	src, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to open gadget binary: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing: %w", dstPath, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("failed to write gadget binary via mount: %w", err)
	}
	dst.Close()
	_ = os.Chmod(dstPath, 0755)

	flavourPath := filepath.Join(tmpDir, ".image-flavour")
	if _, err := os.Stat(flavourPath); os.IsNotExist(err) && flavour != "" {
		if err := os.WriteFile(flavourPath, []byte(flavour), 0444); err != nil {
			logger.Debug("Failed to persist .image-flavour via mount; continuing", "error", err)
		}
	}

	if out, err := exec.Command("sync").CombinedOutput(); err != nil {
		logger.Debug("sync failed after mount write", "error", err, "output", string(out))
	}

	return nil
}

func main() {
	logger := slog.Default()

	var source string
	var appBinary string
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

		switch kind {
		case UpdateKindFull:
			if err := performUpdate(source, destination, kind, logger); err != nil {
				logger.Error("Update failed", "error", err)
				os.Exit(1)
			}
		case UpdateKindAppOnly:
			appBinary = source
			if err := performAppBinaryUpdate(appBinary, destination, logger); err != nil {
				logger.Error("Update failed", "error", err)
				os.Exit(1)
			}
		default:
			logger.Error("Invalid update kind. Valid options are: full, app")
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
		switch kind {
		case UpdateKindFull:
			flavour, err := deviceFlavour(selectedDevice.Path)
			if err != nil {
				logger.Error("Failed to detect device flavor", "error", err)
				os.Exit(1)
			}
			url := fmt.Sprintf("%s%s.img.xz", constants.LatestReleaseURL, flavour)
			downloaded, cleanupFn, err := downloadWithProgress(url)
			if err != nil {
				logger.Error("Failed to download image", "error", err)
				os.Exit(1)
			}
			defer cleanupFn()
			source = downloaded
		case UpdateKindAppOnly:
			url := fmt.Sprintf("%s%s", constants.LatestReleaseURL, constants.AppBinaryName)
			downloaded, cleanupFn, err := downloadWithProgress(url)
			if err != nil {
				logger.Error("Failed to download gadget binary", "error", err)
				os.Exit(1)
			}
			defer cleanupFn()
			appBinary = downloaded
		default:
			logger.Error("Unsupported update kind", "kind", kind)
			os.Exit(1)
		}
	}

	if kind == UpdateKindFull {
		if _, err := os.Stat(source); err != nil {
			logger.Error("Invalid source image", "error", err)
			os.Exit(1)
		}
	} else if kind == UpdateKindAppOnly {
		if _, err := os.Stat(appBinary); err != nil {
			logger.Error("Invalid gadget binary", "error", err)
			os.Exit(1)
		}
	}

	fmt.Printf("Updating %s with a %s update...\n\n", selectedDevice.Path, string(kind))

	switch kind {
	case UpdateKindFull:
		if err := performUpdate(source, selectedDevice.Path, kind, logger); err != nil {
			logger.Error("Update failed", "error", err)
			os.Exit(1)
		}
	case UpdateKindAppOnly:
		if err := performAppBinaryUpdate(appBinary, selectedDevice.Path, logger); err != nil {
			logger.Error("Update failed", "error", err)
			os.Exit(1)
		}
	default:
		logger.Error("Unsupported update kind", "kind", kind)
		os.Exit(1)
	}

	fmt.Println("✅ Update completed successfully")
}
