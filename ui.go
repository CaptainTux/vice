// ui.go
// Copyright(c) 2022 Matt Pharr, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"os"
	"path"
	"runtime/debug"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/mmp/imgui-go/v4"
	"github.com/pkg/browser"
)

var (
	ui struct {
		font      *Font
		aboutFont *Font

		menuBarHeight float32

		showAboutDialog bool

		iconTextureID     uint32
		sadTowerTextureID uint32

		jsonSelectDialog *FileSelectDialogBox

		activeModalDialogs []*ModalDialogBox

		newReleaseDialogChan chan *NewReleaseModalClient
	}

	//go:embed icons/tower-256x256.png
	iconPNG string
	//go:embed icons/sad-tower-alpha-128x128.png
	sadTowerPNG string

	whatsNew []string = []string{
		"Added EWR scenarios, including both departure and approach.",
		"Added Liberty departure scenarios.",
		"Improved routing of departures beyond their exit fix.",
		"Fixed a bug where aircraft on RNAV arrivals wouldn't descend.",
		"Each scenario has a default video map, selected automatically.",
		"If an aircraft given approach clearance is later vectored, approach clearance is now canceled.",
		"Improved spawn positions and hand-off locations for JFK arrivals.",
	}
)

var UIControlColor RGB = RGB{R: 0.2754237, G: 0.2754237, B: 0.2754237}
var UICautionColor RGB = RGBFromHex(0xB7B513)
var UITextColor RGB = RGB{R: 0.85, G: 0.85, B: 0.85}
var UITextHighlightColor RGB = RGBFromHex(0xB2B338)
var UIErrorColor RGB = RGBFromHex(0xE94242)

func imguiInit() *imgui.Context {
	context := imgui.CreateContext(nil)
	imgui.CurrentIO().SetIniFilename("")

	// General imgui styling
	style := imgui.CurrentStyle()
	style.SetFrameRounding(2.)
	style.SetWindowRounding(4.)
	style.SetPopupRounding(4.)
	style.SetScrollbarSize(6.)
	style.ScaleAllSizes(1.25)

	return context
}

func uiInit(renderer Renderer) {
	ui.font = GetFont(FontIdentifier{Name: "Roboto Regular", Size: 16})
	ui.aboutFont = GetFont(FontIdentifier{Name: "Roboto Regular", Size: 18})

	if iconImage, err := png.Decode(bytes.NewReader([]byte(iconPNG))); err != nil {
		lg.Errorf("Unable to decode icon PNG: %v", err)
	} else {
		ui.iconTextureID = renderer.CreateTextureFromImage(iconImage)
	}

	if sadTowerImage, err := png.Decode(bytes.NewReader([]byte(sadTowerPNG))); err != nil {
		lg.Errorf("Unable to decode sad tower PNG: %v", err)
	} else {
		ui.sadTowerTextureID = renderer.CreateTextureFromImage(sadTowerImage)
	}

	// Do this asynchronously since it involves network traffic and may
	// take some time (or may even time out, etc.)
	ui.newReleaseDialogChan = make(chan *NewReleaseModalClient)
	go checkForNewRelease(ui.newReleaseDialogChan)

	if globalConfig.WhatsNewIndex < len(whatsNew) {
		uiShowModalDialog(NewModalDialogBox(&WhatsNewModalClient{}), false)
	}

	uiShowModalDialog(NewModalDialogBox(&ConnectModalClient{}), false)
}

func uiShowModalDialog(d *ModalDialogBox, atFront bool) {
	if atFront {
		ui.activeModalDialogs = append([]*ModalDialogBox{d}, ui.activeModalDialogs...)
	} else {
		ui.activeModalDialogs = append(ui.activeModalDialogs, d)
	}
}

// If |b| is true, all following imgui elements will be disabled (and drawn
// accordingly).
func uiStartDisable(b bool) {
	if b {
		imgui.PushItemFlag(imgui.ItemFlagsDisabled, true)
		imgui.PushStyleVarFloat(imgui.StyleVarAlpha, imgui.CurrentStyle().Alpha()*0.5)
	}
}

// Each call to uiStartDisable should have a matching call to uiEndDisable,
// with the same Boolean value passed to it.
func uiEndDisable(b bool) {
	if b {
		imgui.PopItemFlag()
		imgui.PopStyleVar()
	}
}

func (c RGB) imgui() imgui.Vec4 {
	return imgui.Vec4{c.R, c.G, c.B, 1}
}

func drawUI(platform Platform) {
	if ui.newReleaseDialogChan != nil {
		select {
		case dialog, ok := <-ui.newReleaseDialogChan:
			if ok {
				uiShowModalDialog(NewModalDialogBox(dialog), false)
			} else {
				// channel was closed
				ui.newReleaseDialogChan = nil
			}
		default:
			// don't block on the chan if there's nothing there and it's still open...
		}
	}

	imgui.PushFont(ui.font.ifont)
	if imgui.BeginMainMenuBar() {
		if imgui.BeginMenu("Simulation") {
			if sim.Paused() {
				if imgui.MenuItem("Resume") {
					sim.TogglePause()
				}
			} else {
				if imgui.MenuItem("Pause") {
					sim.TogglePause()
				}
			}
			if imgui.MenuItem("Restart...") {
				uiShowModalDialog(NewModalDialogBox(&ConnectModalClient{}), false)
			}
			imgui.Separator()
			if imgui.MenuItem("Settings...") {
				sim.ActivateSettingsWindow()
			}
			imgui.EndMenu()
		}

		if imgui.BeginMenu("Help") {
			if imgui.MenuItem("Documentation...") {
				browser.OpenURL("https://pharr.org/vice/index.html")
			}
			if imgui.MenuItem("Report a bug...") {
				browser.OpenURL("https://pharr.org/vice/index.html#bugs")
			}
			imgui.Separator()
			if imgui.MenuItem("About vice...") {
				ui.showAboutDialog = true
			}
			imgui.EndMenu()
		}

		t := FontAwesomeIconDiscord
		width, _ := ui.font.BoundText(t, 0)
		imgui.SetCursorPos(imgui.Vec2{platform.DisplaySize()[0] - float32(width+10), 0})
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.CurrentStyle().Color(imgui.StyleColorMenuBarBg))
		if imgui.Button(t) {
			browser.OpenURL("https://discord.gg/y993vgQxhY")
		}
		imgui.PopStyleColor()

		imgui.EndMainMenuBar()
	}
	ui.menuBarHeight = imgui.CursorPos().Y - 1

	sim.DrawSettingsWindow()

	drawActiveDialogBoxes()

	wmDrawUI(platform)

	imgui.PopFont()

	// Finalize and submit the imgui draw lists
	imgui.Render()
	cb := GetCommandBuffer()
	defer ReturnCommandBuffer(cb)
	GenerateImguiCommandBuffer(cb)
	stats.renderUI = renderer.RenderCommandBuffer(cb)
}

func drawActiveDialogBoxes() {
	for len(ui.activeModalDialogs) > 0 {
		d := ui.activeModalDialogs[0]
		if !d.closed {
			d.Draw()
			break
		} else {
			ui.activeModalDialogs = ui.activeModalDialogs[1:]
		}
	}

	if ui.showAboutDialog {
		showAboutDialog()
	}
}

func setCursorForRightButtons(text []string) {
	style := imgui.CurrentStyle()
	width := float32(0)

	for i, t := range text {
		width += imgui.CalcTextSize(t, false, 100000).X + 2*style.FramePadding().X
		if i > 0 {
			// space between buttons
			width += style.ItemSpacing().X
		}
	}
	offset := imgui.ContentRegionAvail().X - width
	imgui.SetCursorPos(imgui.Vec2{offset, imgui.CursorPosY()})
}

///////////////////////////////////////////////////////////////////////////

func drawAirportSelector(airports map[string]interface{}, title string) (map[string]interface{}, bool) {
	airportsString := strings.Join(SortedMapKeys(airports), ",")

	if imgui.InputTextV(title, &airportsString, imgui.InputTextFlagsCharsUppercase, nil) {
		ap := strings.FieldsFunc(airportsString, func(ch rune) bool {
			return unicode.IsSpace(ch) || ch == ','
		})

		airports = make(map[string]interface{})
		for _, a := range ap {
			airports[a] = nil
		}
		return airports, true
	}

	return airports, false
}

///////////////////////////////////////////////////////////////////////////

type ComboBoxState struct {
	inputValues  []*string
	selected     map[string]interface{}
	lastSelected *string
}

func NewComboBoxState(nentry int) *ComboBoxState {
	s := &ComboBoxState{}
	for i := 0; i < nentry; i++ {
		s.inputValues = append(s.inputValues, new(string))
	}
	s.selected = make(map[string]interface{})
	s.lastSelected = new(string)
	return s
}

type ComboBoxDisplayConfig struct {
	ColumnHeaders    []string
	DrawHeaders      bool
	EntryNames       []string
	InputFlags       []imgui.InputTextFlags
	SelectAllColumns bool
	Size             imgui.Vec2
	MaxDisplayed     int
	FixedDisplayed   int
}

func DrawComboBox(state *ComboBoxState, config ComboBoxDisplayConfig,
	firstColumn []string, drawColumn func(s string, col int),
	inputValid func([]*string) bool, add func([]*string), deleteSelection func(map[string]interface{})) {
	id := fmt.Sprintf("%p", state)
	flags := imgui.TableFlagsBordersH | imgui.TableFlagsBordersOuterV | imgui.TableFlagsRowBg

	sz := config.Size
	if config.FixedDisplayed != 0 {
		flags = flags | imgui.TableFlagsScrollY
		sz.Y = float32(config.FixedDisplayed * (4 + ui.font.size))
	} else if config.MaxDisplayed == 0 || len(firstColumn) < config.MaxDisplayed {
		sz.Y = 0
	} else {
		flags = flags | imgui.TableFlagsScrollY
		sz.Y = float32((1 + config.MaxDisplayed) * (6 + ui.font.size))
	}

	if imgui.BeginTableV("##"+id, len(config.ColumnHeaders), flags, sz, 0.0) {
		for _, name := range config.ColumnHeaders {
			imgui.TableSetupColumn(name)
		}
		if config.DrawHeaders {
			imgui.TableHeadersRow()
		}

		io := imgui.CurrentIO()
		for _, entry := range firstColumn {
			imgui.TableNextRow()
			imgui.TableNextColumn()
			_, isSelected := state.selected[entry]
			var selFlags imgui.SelectableFlags
			if config.SelectAllColumns {
				selFlags = imgui.SelectableFlagsSpanAllColumns
			}
			if imgui.SelectableV(entry, isSelected, selFlags, imgui.Vec2{}) {
				if io.KeyCtrlPressed() {
					// Toggle selection of this one
					if isSelected {
						delete(state.selected, entry)
					} else {
						state.selected[entry] = nil
						*state.lastSelected = entry
					}
				} else if io.KeyShiftPressed() {
					for _, e := range firstColumn {
						if entry > *state.lastSelected {
							if e > *state.lastSelected && e <= entry {
								state.selected[e] = nil
							}
						} else {
							if e >= entry && e <= *state.lastSelected {
								state.selected[e] = nil
							}
						}
					}
					*state.lastSelected = entry
				} else {
					// Select only this one
					for k := range state.selected {
						delete(state.selected, k)
					}
					state.selected[entry] = nil
					*state.lastSelected = entry
				}
			}
			for i := 1; i < len(config.ColumnHeaders); i++ {
				imgui.TableNextColumn()
				drawColumn(entry, i)
			}
		}
		imgui.EndTable()
	}

	valid := inputValid(state.inputValues)
	for i, entry := range config.EntryNames {
		flags := imgui.InputTextFlagsEnterReturnsTrue
		if config.InputFlags != nil {
			flags |= config.InputFlags[i]
		}
		if imgui.InputTextV(entry+"##"+id, state.inputValues[i], flags, nil) && valid {
			add(state.inputValues)
			for _, s := range state.inputValues {
				*s = ""
			}
			imgui.SetKeyboardFocusHereV(-1)
		}
	}

	uiStartDisable(!valid)
	imgui.SameLine()
	if imgui.Button("+##" + id) {
		add(state.inputValues)
		for _, s := range state.inputValues {
			*s = ""
		}
	}
	uiEndDisable(!valid)

	enableDelete := len(state.selected) > 0
	uiStartDisable(!enableDelete)
	imgui.SameLine()
	if imgui.Button(FontAwesomeIconTrash + "##" + id) {
		deleteSelection(state.selected)
		for k := range state.selected {
			delete(state.selected, k)
		}
	}
	uiEndDisable(!enableDelete)
}

///////////////////////////////////////////////////////////////////////////

type ModalDialogBox struct {
	closed, isOpen bool
	client         ModalDialogClient
}

type ModalDialogButton struct {
	text     string
	disabled bool
	action   func() bool
}

type ModalDialogClient interface {
	Title() string
	Opening()
	Buttons() []ModalDialogButton
	Draw() int /* returns index of equivalently-clicked button; out of range if none */
}

func NewModalDialogBox(c ModalDialogClient) *ModalDialogBox {
	return &ModalDialogBox{client: c}
}

func (m *ModalDialogBox) Draw() {
	if m.closed {
		return
	}

	title := fmt.Sprintf("%s##%p", m.client.Title(), m)
	if !m.isOpen {
		imgui.OpenPopup(title)
	}

	flags := imgui.WindowFlagsNoResize | imgui.WindowFlagsAlwaysAutoResize | imgui.WindowFlagsNoSavedSettings
	if imgui.BeginPopupModalV(title, nil, flags) {
		if !m.isOpen {
			imgui.SetKeyboardFocusHere()
			m.client.Opening()
			m.isOpen = true
		}

		selIndex := m.client.Draw()
		imgui.Text("\n") // spacing

		buttons := m.client.Buttons()

		// First, figure out where to start drawing so the buttons end up right-justified.
		// https://github.com/ocornut/imgui/discussions/3862
		var allButtonText []string
		for _, b := range buttons {
			allButtonText = append(allButtonText, b.text)
		}
		setCursorForRightButtons(allButtonText)

		for i, b := range buttons {
			uiStartDisable(b.disabled)
			if i > 0 {
				imgui.SameLine()
			}
			if (imgui.Button(b.text) || i == selIndex) && !b.disabled {
				if b.action == nil || b.action() {
					imgui.CloseCurrentPopup()
					m.closed = true
					m.isOpen = false
				}
			}
			uiEndDisable(b.disabled)
		}

		imgui.EndPopup()
	}
}

type ConnectModalClient struct {
	connectionType ConnectionType
	err            string

	sim SimConnectionConfiguration
}

type ConnectionType int

const (
	ConnectionTypeSimServer = iota
	ConnectionTypeCount
)

func (c ConnectionType) String() string {
	return [...]string{"Sim Server"}[c]
}

func (c *ConnectModalClient) Title() string { return "New Simulation" }

func (c *ConnectModalClient) Opening() {
	c.connectionType = ConnectionTypeSimServer
	c.err = ""
	c.sim.Initialize()
}

func (c *ConnectModalClient) Buttons() []ModalDialogButton {
	var b []ModalDialogButton
	b = append(b, ModalDialogButton{text: "Cancel"})

	ok := ModalDialogButton{text: "Ok", action: func() bool {
		var err error
		switch c.connectionType {
		case ConnectionTypeSimServer:
			err = c.sim.Connect()

		default:
			lg.Errorf("Unhandled connection type")
			return true
		}

		if err == nil {
			c.err = ""
		} else {
			c.err = err.Error()
		}
		return err == nil
	}}

	switch c.connectionType {
	case ConnectionTypeSimServer:
		ok.disabled = !c.sim.Valid()

	default:
		lg.Errorf("Unhandled connection type")
	}
	b = append(b, ok)

	return b
}

func (c *ConnectModalClient) Draw() int {
	/*
		if imgui.BeginComboV("Type", c.connectionType.String(), imgui.ComboFlagsHeightLarge) {
			for i := 0; i < ConnectionTypeCount; i++ {
				ct := ConnectionType(i)
				if imgui.SelectableV(ct.String(), ct == c.connectionType, 0, imgui.Vec2{}) {
					c.connectionType = ct
				}
			}
			imgui.EndCombo()
		}
	*/
	var enter bool
	switch c.connectionType {
	case ConnectionTypeSimServer:
		enter = c.sim.DrawUI()
	}

	if c.err != "" {
		imgui.Text(c.err)
	}

	if enter {
		return 1
	} else {
		return -1
	}
}

type DisconnectModalClient struct{}

func (d *DisconnectModalClient) Title() string { return "Confirm Disconnection" }

func (d *DisconnectModalClient) Opening() {
}

func (c *DisconnectModalClient) Buttons() []ModalDialogButton {
	var b []ModalDialogButton
	b = append(b, ModalDialogButton{text: "Cancel"})

	ok := ModalDialogButton{text: "Ok", action: func() bool {
		sim.Disconnect()
		return true
	}}
	b = append(b, ok)

	return b
}

func (d *DisconnectModalClient) Draw() int {
	imgui.Text("Are you sure you want to disconnect?")
	return -1
}

type YesOrNoModalClient struct {
	title, query string
	ok, notok    func()
}

func (yn *YesOrNoModalClient) Title() string { return yn.title }

func (yn *YesOrNoModalClient) Opening() {}

func (yn *YesOrNoModalClient) Buttons() []ModalDialogButton {
	var b []ModalDialogButton
	b = append(b, ModalDialogButton{text: "No", action: func() bool {
		if yn.notok != nil {
			yn.notok()
		}
		return true
	}})
	b = append(b, ModalDialogButton{text: "Yes", action: func() bool {
		if yn.ok != nil {
			yn.ok()
		}
		return true
	}})
	return b
}

func (yn *YesOrNoModalClient) Draw() int {
	imgui.Text(yn.query)
	return -1
}

func checkForNewRelease(newReleaseDialogChan chan *NewReleaseModalClient) {
	defer close(newReleaseDialogChan)

	url := "https://api.github.com/repos/mmp/vice/releases"

	resp, err := http.Get(url)
	if err != nil {
		lg.Errorf("%s: get err: %v", url, err)
		return
	}
	defer resp.Body.Close()

	type Release struct {
		TagName string    `json:"tag_name"`
		Created time.Time `json:"created_at"`
	}

	decoder := json.NewDecoder(resp.Body)
	var releases []Release
	if err := decoder.Decode(&releases); err != nil {
		lg.Errorf("JSON decode error: %v", err)
		return
	}
	if len(releases) == 0 {
		return
	}

	var newestRelease *Release
	for i := range releases {
		if strings.HasSuffix(releases[i].TagName, "-beta") {
			continue
		}
		if newestRelease == nil || releases[i].Created.After(newestRelease.Created) {
			newestRelease = &releases[i]
		}
	}
	if newestRelease == nil {
		lg.Errorf("No vice releases found?")
		return
	}

	lg.Printf("newest release found: %v", newestRelease)

	buildTime := ""
	if bi, ok := debug.ReadBuildInfo(); !ok {
		lg.Errorf("unable to read build info")
		return
	} else {
		for _, setting := range bi.Settings {
			if setting.Key == "vcs.time" {
				buildTime = setting.Value
				break
			}
		}

		if buildTime == "" {
			lg.Errorf("build time unavailable in BuildInfo.Settings")
			return
		}
	}

	if bt, err := time.Parse(time.RFC3339, buildTime); err != nil {
		lg.Errorf("error parsing build time \"%s\": %v", buildTime, err)
	} else if newestRelease.Created.UTC().After(bt.UTC()) {
		lg.Printf("build time %s newest release %s -> release is newer",
			bt.UTC().String(), newestRelease.Created.UTC().String())
		newReleaseDialogChan <- &NewReleaseModalClient{
			version: newestRelease.TagName,
			date:    newestRelease.Created}
	} else {
		lg.Printf("build time %s newest release %s -> build is newer",
			bt.UTC().String(), newestRelease.Created.UTC().String())
	}
}

type NewReleaseModalClient struct {
	version string
	date    time.Time
}

func (nr *NewReleaseModalClient) Title() string {
	return "A new vice release is available"
}
func (nr *NewReleaseModalClient) Opening() {}

func (nr *NewReleaseModalClient) Buttons() []ModalDialogButton {
	return []ModalDialogButton{
		ModalDialogButton{
			text: "Quit and update",
			action: func() bool {
				browser.OpenURL("https://pharr.org/vice/index.html#section-installation")
				os.Exit(0)
				return true
			},
		},
		ModalDialogButton{text: "Update later"}}
}

func (nr *NewReleaseModalClient) Draw() int {
	imgui.Text(fmt.Sprintf("vice version %s is the latest version", nr.version))
	imgui.Text("Would you like to quit and open the vice downloads page?")
	return -1
}

type WhatsNewModalClient struct{}

func (nr *WhatsNewModalClient) Title() string {
	return "What's new in this version of vice"
}

func (nr *WhatsNewModalClient) Opening() {}

func (nr *WhatsNewModalClient) Buttons() []ModalDialogButton {
	return []ModalDialogButton{
		ModalDialogButton{
			text: "Ok",
			action: func() bool {
				globalConfig.WhatsNewIndex = len(whatsNew)
				return true
			},
		},
	}
}

func (nr *WhatsNewModalClient) Draw() int {
	for i := globalConfig.WhatsNewIndex; i < len(whatsNew); i++ {
		imgui.Text(FontAwesomeIconSquare + " " + whatsNew[i])
	}
	return -1
}

///////////////////////////////////////////////////////////////////////////
// "about" dialog box

func showAboutDialog() {
	flags := imgui.WindowFlagsNoResize | imgui.WindowFlagsNoSavedSettings
	imgui.BeginV("About vice...", &ui.showAboutDialog, flags)

	imgui.Image(imgui.TextureID(ui.iconTextureID), imgui.Vec2{256, 256})

	center := func(s string) {
		// https://stackoverflow.com/a/67855985
		ww := imgui.WindowSize().X
		tw := imgui.CalcTextSize(s, false, 0).X
		imgui.SetCursorPos(imgui.Vec2{(ww - tw) * 0.5, imgui.CursorPosY()})
		imgui.Text(s)
	}

	imgui.PushFont(ui.aboutFont.ifont)
	center("vice")
	center(FontAwesomeIconCopyright + "2023 Matt Pharr")
	center("Licensed under the GPL, Version 3")
	if imgui.IsItemHovered() && imgui.IsMouseClicked(0) {
		browser.OpenURL("https://www.gnu.org/licenses/gpl-3.0.html")
	}
	center("Current build: " + buildVersion)
	center("Source code: " + FontAwesomeIconGithub)
	if imgui.IsItemHovered() && imgui.IsMouseClicked(0) {
		browser.OpenURL("https://github.com/mmp/vice")
	}

	imgui.PopFont()

	imgui.End()
}

///////////////////////////////////////////////////////////////////////////
// FileSelectDialogBox

type FileSelectDialogBox struct {
	show, isOpen bool
	filename     string
	directory    string

	dirEntries            []DirEntry
	dirEntriesLastUpdated time.Time

	selectDirectory bool
	title           string
	filter          []string
	callback        func(string)
}

type DirEntry struct {
	name  string
	isDir bool
}

func NewFileSelectDialogBox(title string, filter []string, filename string,
	callback func(string)) *FileSelectDialogBox {
	return &FileSelectDialogBox{
		title:     title,
		directory: defaultDirectory(filename),
		filter:    filter,
		callback:  callback}
}

func NewDirectorySelectDialogBox(title string, current string,
	callback func(string)) *FileSelectDialogBox {
	fsd := &FileSelectDialogBox{
		title:           title,
		selectDirectory: true,
		callback:        callback}
	if current != "" {
		fsd.directory = current
	} else {
		fsd.directory = defaultDirectory("")
	}
	return fsd
}

func defaultDirectory(filename string) string {
	var dir string
	if filename != "" {
		dir = path.Dir(filename)
	} else {
		var err error
		if dir, err = os.UserHomeDir(); err != nil {
			lg.Errorf("Unable to get user home directory: %v", err)
			dir = "."
		}
	}
	return path.Clean(dir)
}

func (fs *FileSelectDialogBox) Activate() {
	fs.show = true
	fs.isOpen = false
}

func (fs *FileSelectDialogBox) Draw() {
	if !fs.show {
		return
	}

	if !fs.isOpen {
		imgui.OpenPopup(fs.title)
	}

	flags := imgui.WindowFlagsNoResize | imgui.WindowFlagsAlwaysAutoResize | imgui.WindowFlagsNoSavedSettings
	if imgui.BeginPopupModalV(fs.title, nil, flags) {
		if !fs.isOpen {
			imgui.SetKeyboardFocusHere()
			fs.isOpen = true
		}

		if imgui.Button(FontAwesomeIconHome) {
			var err error
			fs.directory, err = os.UserHomeDir()
			if err != nil {
				lg.Errorf("Unable to get user home dir: %v", err)
				fs.directory = "."
			}
		}
		imgui.SameLine()
		if imgui.Button(FontAwesomeIconLevelUpAlt) {
			fs.directory, _ = path.Split(fs.directory)
			fs.directory = path.Clean(fs.directory) // get rid of trailing slash
			fs.dirEntriesLastUpdated = time.Time{}
			fs.filename = ""
		}

		imgui.SameLine()
		imgui.Text(fs.directory)

		// Only rescan the directory contents once a second.
		if time.Since(fs.dirEntriesLastUpdated) > 1*time.Second {
			if dirEntries, err := os.ReadDir(fs.directory); err != nil {
				lg.Errorf("%s: unable to read directory: %v", fs.directory, err)
			} else {
				fs.dirEntries = nil
				for _, entry := range dirEntries {
					if entry.Type()&os.ModeSymlink != 0 {
						info, err := os.Stat(path.Join(fs.directory, entry.Name()))
						if err == nil {
							e := DirEntry{name: entry.Name(), isDir: info.IsDir()}
							fs.dirEntries = append(fs.dirEntries, e)
						} else {
							e := DirEntry{name: entry.Name(), isDir: false}
							fs.dirEntries = append(fs.dirEntries, e)
						}
					} else {
						e := DirEntry{name: entry.Name(), isDir: entry.IsDir()}
						fs.dirEntries = append(fs.dirEntries, e)
					}
				}
				sort.Slice(fs.dirEntries, func(i, j int) bool {
					return fs.dirEntries[i].name < fs.dirEntries[j].name
				})
			}
			fs.dirEntriesLastUpdated = time.Now()
		}

		flags := imgui.TableFlagsScrollY | imgui.TableFlagsRowBg
		fileSelected := false
		// unique per-directory id maintains the scroll position in each
		// directory (and starts newly visited ones at the top!)
		if imgui.BeginTableV("Files##"+fs.directory, 1, flags,
			imgui.Vec2{500, float32(platform.WindowSize()[1] * 3 / 4)}, 0) {
			imgui.TableSetupColumn("Filename")
			for _, entry := range fs.dirEntries {
				icon := ""
				if entry.isDir {
					icon = FontAwesomeIconFolder
				} else {
					icon = FontAwesomeIconFile
				}

				canSelect := entry.isDir
				if !entry.isDir {
					if fs.filter == nil && !fs.selectDirectory {
						canSelect = true
					}
					for _, f := range fs.filter {
						if strings.HasSuffix(strings.ToUpper(entry.name), strings.ToUpper(f)) {
							canSelect = true
							break
						}
					}
				}

				uiStartDisable(!canSelect)
				imgui.TableNextRow()
				imgui.TableNextColumn()
				selFlags := imgui.SelectableFlagsSpanAllColumns
				if imgui.SelectableV(icon+" "+entry.name, entry.name == fs.filename, selFlags, imgui.Vec2{}) {
					fs.filename = entry.name
				}
				if imgui.IsItemHovered() && imgui.IsMouseDoubleClicked(0) {
					if entry.isDir {
						fs.directory = path.Join(fs.directory, entry.name)
						fs.filename = ""
						fs.dirEntriesLastUpdated = time.Time{}
					} else {
						fileSelected = true
					}
				}
				uiEndDisable(!canSelect)
			}
			imgui.EndTable()
		}

		if imgui.Button("Cancel") {
			imgui.CloseCurrentPopup()
			fs.show = false
			fs.isOpen = false
			fs.filename = ""
		}

		disableOk := fs.filename == "" && !fs.selectDirectory
		uiStartDisable(disableOk)
		imgui.SameLine()
		if imgui.Button("Ok") || fileSelected {
			imgui.CloseCurrentPopup()
			fs.show = false
			fs.isOpen = false
			fs.callback(path.Join(fs.directory, fs.filename))
			fs.filename = ""
		}
		uiEndDisable(disableOk)

		imgui.EndPopup()
	}
}

type ErrorModalClient struct {
	message string
}

func (e *ErrorModalClient) Title() string { return "Vice Error" }
func (e *ErrorModalClient) Opening()      {}

func (e *ErrorModalClient) Buttons() []ModalDialogButton {
	var b []ModalDialogButton
	b = append(b, ModalDialogButton{text: "Ok", action: func() bool {
		return true
	}})
	return b
}

func (e *ErrorModalClient) Draw() int {
	if imgui.BeginTableV("Error", 2, 0, imgui.Vec2{}, 0) {
		imgui.TableSetupColumn("icon")
		imgui.TableSetupColumn("text")

		imgui.TableNextRow()
		imgui.TableNextColumn()
		imgui.Image(imgui.TextureID(ui.sadTowerTextureID), imgui.Vec2{128, 128})

		imgui.TableNextColumn()
		imgui.Text("\n\n" + e.message)

		imgui.EndTable()
	}
	return -1
}

func ShowErrorDialog(s string, args ...interface{}) {
	d := NewModalDialogBox(&ErrorModalClient{message: fmt.Sprintf(s, args...)})
	uiShowModalDialog(d, true)

	lg.ErrorfUp1(s, args...)
}

func ShowFatalErrorDialog(s string, args ...interface{}) {
	lg.ErrorfUp1(s, args...)

	d := NewModalDialogBox(&ErrorModalClient{message: fmt.Sprintf(s, args...)})

	for !d.closed {
		platform.ProcessEvents()
		platform.NewFrame()
		imgui.NewFrame()
		imgui.PushFont(ui.font.ifont)
		d.Draw()
		imgui.PopFont()

		imgui.Render()
		var cb CommandBuffer
		GenerateImguiCommandBuffer(&cb)
		renderer.RenderCommandBuffer(&cb)

		platform.PostRender()
	}
}

///////////////////////////////////////////////////////////////////////////
// ScrollBar

// ScrollBar provides functionality for a basic scrollbar for use in Pane
// implementations.  (Since those are not handled by imgui, we can't use
// imgui's scrollbar there.)
type ScrollBar struct {
	offset            int
	barWidth          int
	nItems, nVisible  int
	accumDrag         float32
	invertY           bool
	mouseClickedInBar bool
}

// NewScrollBar returns a new ScrollBar instance with the given width.
// invertY indicates whether the scrolled items are drawn from the bottom
// of the Pane or the top; invertY should be true if they are being drawn
// from the bottom.
func NewScrollBar(width int, invertY bool) *ScrollBar {
	return &ScrollBar{barWidth: width, invertY: invertY}
}

// Update should be called once per frame, providing the total number of things
// being drawn, the number of them that are visible, and the PaneContext passed
// to the Pane's Draw method (so that mouse events can be handled, if appropriate.
func (sb *ScrollBar) Update(nItems int, nVisible int, ctx *PaneContext) {
	sb.nItems = nItems
	sb.nVisible = nVisible

	if sb.nItems > sb.nVisible {
		sign := float32(1)
		if sb.invertY {
			sign = -1
		}

		if ctx.mouse != nil {
			sb.offset += int(sign * ctx.mouse.Wheel[1])

			if ctx.mouse.Clicked[0] {
				sb.mouseClickedInBar = ctx.mouse.Pos[0] >= ctx.paneExtent.Width()-float32(sb.Width())
				sb.accumDrag = 0
			}

			if ctx.mouse.Dragging[0] && sb.mouseClickedInBar {
				sb.accumDrag += -sign * ctx.mouse.DragDelta[1] * float32(sb.nItems) / ctx.paneExtent.Height()
				if abs(sb.accumDrag) >= 1 {
					sb.offset += int(sb.accumDrag)
					sb.accumDrag -= float32(int(sb.accumDrag))
				}
			}
		}
		sb.offset = clamp(sb.offset, 0, sb.nItems-sb.nVisible)
	} else {
		sb.offset = 0
	}
}

// Offset returns the offset into the items at which drawing should start
// (i.e., the items before the offset are offscreen.)  Note that the scroll
// offset is reported in units of the number of items passed to Update;
// thus, if scrolling text, the number of items might be measured in lines
// of text, or it might be measured in scanlines.  The choice determines
// whether scrolling happens at the granularity of entire lines at a time
// or is continuous.
func (sb *ScrollBar) Offset() int {
	return sb.offset
}

// Visible indicates whether the scrollbar will be drawn (it disappears if
// all of the items can fit onscreen.)
func (sb *ScrollBar) Visible() bool {
	return sb.nItems > sb.nVisible
}

// Draw emits the drawing commands for the scrollbar into the provided
// CommandBuffer.
func (sb *ScrollBar) Draw(ctx *PaneContext, cb *CommandBuffer) {
	if !sb.Visible() {
		return
	}

	pw, ph := ctx.paneExtent.Width(), ctx.paneExtent.Height()
	// The visible region is [offset,offset+nVisible].
	// Visible region w.r.t. [0,1]
	y0, y1 := float32(sb.offset)/float32(sb.nItems), float32(sb.offset+sb.nVisible)/float32(sb.nItems)
	if sb.invertY {
		y0, y1 = 1-y0, 1-y1
	}
	// Visible region in window coordinates
	const edgeSpace = 2
	wy0, wy1 := lerp(y0, ph-edgeSpace, edgeSpace), lerp(y1, ph-edgeSpace, edgeSpace)

	quad := GetColoredTrianglesDrawBuilder()
	defer ReturnColoredTrianglesDrawBuilder(quad)
	quad.AddQuad([2]float32{pw - float32(sb.barWidth) - float32(edgeSpace), wy0},
		[2]float32{pw - float32(edgeSpace), wy0},
		[2]float32{pw - float32(edgeSpace), wy1},
		[2]float32{pw - float32(sb.barWidth) - float32(edgeSpace), wy1}, UIControlColor)
	quad.GenerateCommands(cb)
}

func (sb *ScrollBar) Width() int {
	return sb.barWidth + 4 /* for edge space... */
}

///////////////////////////////////////////////////////////////////////////
// Text editing

const (
	TextEditReturnNone = iota
	TextEditReturnTextChanged
	TextEditReturnEnter
	TextEditReturnNext
	TextEditReturnPrev
)

// uiDrawTextEdit handles the basics of interactive text editing; it takes
// a string and cursor position and then renders them with the specified
// style, processes keyboard inputs and updates the string accordingly.
func uiDrawTextEdit(s *string, cursor *int, keyboard *KeyboardState, pos [2]float32, style,
	cursorStyle TextStyle, cb *CommandBuffer) (exit int, posOut [2]float32) {
	// Make sure we can depend on it being sensible for the following
	*cursor = clamp(*cursor, 0, len(*s))
	originalText := *s

	// Draw the text and the cursor
	td := GetTextDrawBuilder()
	defer ReturnTextDrawBuilder(td)
	if *cursor == len(*s) {
		// cursor at the end
		posOut = td.AddTextMulti([]string{*s, " "}, pos, []TextStyle{style, cursorStyle})
	} else {
		// cursor in the middle
		sb, sc, se := (*s)[:*cursor], (*s)[*cursor:*cursor+1], (*s)[*cursor+1:]
		styles := []TextStyle{style, cursorStyle, style}
		posOut = td.AddTextMulti([]string{sb, sc, se}, pos, styles)
	}
	td.GenerateCommands(cb)

	// Handle various special keys.
	if keyboard != nil {
		if keyboard.IsPressed(KeyBackspace) && *cursor > 0 {
			*s = (*s)[:*cursor-1] + (*s)[*cursor:]
			*cursor--
		}
		if keyboard.IsPressed(KeyDelete) && *cursor < len(*s)-1 {
			*s = (*s)[:*cursor] + (*s)[*cursor+1:]
		}
		if keyboard.IsPressed(KeyLeftArrow) {
			*cursor = max(*cursor-1, 0)
		}
		if keyboard.IsPressed(KeyRightArrow) {
			*cursor = min(*cursor+1, len(*s))
		}
		if keyboard.IsPressed(KeyEscape) {
			// clear out the string
			*s = ""
			*cursor = 0
		}
		if keyboard.IsPressed(KeyEnter) {
			wmReleaseKeyboardFocus()
			exit = TextEditReturnEnter
		}
		if keyboard.IsPressed(KeyTab) {
			if keyboard.IsPressed(KeyShift) {
				exit = TextEditReturnPrev
			} else {
				exit = TextEditReturnNext
			}
		}

		// And finally insert any regular characters into the appropriate spot
		// in the string.
		if keyboard.Input != "" {
			*s = (*s)[:*cursor] + keyboard.Input + (*s)[*cursor:]
			*cursor += len(keyboard.Input)
		}
	}

	if exit == TextEditReturnNone && *s != originalText {
		exit = TextEditReturnTextChanged
	}

	return
}
