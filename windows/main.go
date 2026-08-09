package main

import (
	"image"
	"image/color"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/font/gofont"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/trace/windows/catalog"
)

var (
	colInk     = color.NRGBA{R: 0x0D, G: 0x10, B: 0x17, A: 0xFF}
	colMute    = color.NRGBA{R: 0x66, G: 0x70, B: 0x84, A: 0xFF}
	colLine    = color.NRGBA{R: 0xD7, G: 0xDC, B: 0xE5, A: 0xFF}
	colSoft    = color.NRGBA{R: 0xEE, G: 0xF1, B: 0xF6, A: 0xFF}
	colPaper   = color.NRGBA{R: 0xFB, G: 0xFC, B: 0xFE, A: 0xFF}
	colSelect  = color.NRGBA{R: 0xE7, G: 0xEB, B: 0xFF, A: 0xFF}
	colHover   = color.NRGBA{R: 0xF0, G: 0xF3, B: 0xFA, A: 0xFF}
	colSide    = color.NRGBA{R: 0xF7, G: 0xF8, B: 0xFC, A: 0xFF}
	colWhite   = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	colBlue    = color.NRGBA{R: 0x3A, G: 0x5C, B: 0xFF, A: 0xFF}
	colDanger  = color.NRGBA{R: 0xD1, G: 0x43, B: 0x2F, A: 0xFF}
	colTitleBg = color.NRGBA{R: 0xF4, G: 0xF6, B: 0xFA, A: 0xFF}
	colHeader  = color.NRGBA{R: 0xE9, G: 0xEE, B: 0xF5, A: 0xFF}
)

const toolFont = unit.Sp(13)
const titleFont = unit.Sp(13)

type menuItem struct {
	label  string
	action func()
	danger bool
}

type contextMenu struct {
	open   bool
	at     image.Point
	index  int
	hover  int
	items  []menuItem
	clicks []widget.Clickable
	tag    struct{} // fullscreen input trap while menu is open
}

type rowState struct {
	click widget.Clickable
}

type loadResult struct {
	gen        uint64
	programs   []catalog.Program
	err        error
	includeSys bool
}

type scanRow struct {
	hit   catalog.ScanHit
	check widget.Bool
	click widget.Clickable
}

type scanPending struct {
	program catalog.Program
	hits    []catalog.ScanHit
	err     error
	afterUninstall bool
}

type ui struct {
	win   *app.Window
	theme *material.Theme
	deco  widget.Decorations
	maxed bool

	programs   []catalog.Program
	filtered   []catalog.Program
	selected   int
	includeSys bool
	status     string
	hoverRow   int

	search          widget.Editor
	searchOpen      bool
	wantSearchFocus bool
	wantKeyFocus    bool
	list            widget.List
	btnSys          widget.Bool

	rows []rowState
	menu contextMenu

	ptrTag struct{}
	keyTag struct{}
	ptrAt  image.Point

	loading bool
	loadGen uint64
	pending *loadResult
	loadMu  sync.Mutex

	scanOpen    bool
	scanBusy    bool
	scanPhase   string // select | progress | result
	scanProg    catalog.Program
	scanRows    []scanRow
	scanList    widget.List
	btnScanDel  widget.Clickable
	btnScanSkip widget.Clickable
	btnScanAll  widget.Clickable
	btnScanNone widget.Clickable
	btnScanOK   widget.Clickable
	scanPend    *scanPending
	scanDelTotal int
	scanDelDone  int
	scanDelPath  string
	scanResult   string
}

func main() {
	const title = "残影 TRACE — 彻底卸载"
	go func() {
		w := new(app.Window)
		w.Option(
			app.Title(title),
			app.Size(unit.Dp(1120), unit.Dp(740)),
			app.MinSize(unit.Dp(860), unit.Dp(520)),
			app.Decorated(false),
		)
		if err := run(w, title); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}()
	app.Main()
}

func run(w *app.Window, title string) error {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	th.Palette.Fg = colInk
	th.Palette.Bg = colPaper
	th.Palette.ContrastBg = colTitleBg
	th.Palette.ContrastFg = colInk

	u := &ui{
		win:      w,
		theme:    th,
		selected: -1,
		hoverRow: -1,
		status:   "就绪 · 选中后按键搜索 · 右键操作",
		list:     widget.List{List: layout.List{Axis: layout.Vertical}},
		scanList: widget.List{List: layout.List{Axis: layout.Vertical}},
	}
	u.search.SingleLine = true
	u.wantKeyFocus = true
	u.status = "加载中…"
	u.reload() // async — must not block before Event loop

	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.ConfigEvent:
			u.maxed = e.Config.Mode == app.Maximized
			u.deco.Maximized = u.maxed
		case app.ViewEvent:
			if ve, ok := e.(app.Win32ViewEvent); ok && ve.Valid() {
				applyExeIcon(ve.HWND)
			}
		case app.FrameEvent:
			u.takePendingLoad()
			u.takePendingScan()
			gtx := app.NewContext(&ops, e)
			u.update(gtx)
			u.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (u *ui) reload() {
	u.closeMenu()
	u.loadMu.Lock()
	u.loadGen++
	gen := u.loadGen
	u.loadMu.Unlock()
	includeSys := u.includeSys
	u.loading = true
	u.status = "加载中…"
	u.win.Invalidate()

	go func() {
		list, err := catalog.Load(includeSys)
		u.loadMu.Lock()
		u.pending = &loadResult{
			gen:        gen,
			programs:   list,
			err:        err,
			includeSys: includeSys,
		}
		u.loadMu.Unlock()
		u.win.Invalidate()
	}()
}

func (u *ui) takePendingScan() {
	u.loadMu.Lock()
	p := u.scanPend
	u.scanPend = nil
	u.loadMu.Unlock()
	if p == nil {
		return
	}
	u.scanBusy = false
	if p.err != nil {
		msg := p.err.Error()
		if strings.Contains(msg, "未完成") || strings.Contains(msg, "取消") {
			u.status = msg
		} else {
			u.status = "卸载/扫描失败: " + msg
		}
		u.reload()
		return
	}
	if len(p.hits) == 0 {
		if p.afterUninstall {
			u.status = "卸载完成 · 未发现明显残留"
		} else {
			u.status = "未发现明显残留"
		}
		u.closeScan()
		u.reload()
		return
	}
	u.openScan(p.program, p.hits)
}

func (u *ui) takePendingLoad() {
	u.loadMu.Lock()
	p := u.pending
	u.pending = nil
	gen := u.loadGen
	u.loadMu.Unlock()
	if p == nil {
		return
	}
	if p.gen != gen {
		return // stale
	}
	u.loading = false
	if p.err != nil {
		u.status = "加载失败: " + p.err.Error()
		u.programs, u.filtered = nil, nil
		u.selected = -1
		return
	}
	u.programs = p.programs
	u.applyFilter()
	u.refreshStatus()
	if u.selected >= len(u.filtered) {
		u.selected = -1
	}
}

func (u *ui) refreshStatus() {
	if u.searchOpen {
		q := strings.TrimSpace(u.search.Text())
		if q == "" {
			u.status = strconv.Itoa(len(u.filtered)) + " 项 · Esc 关闭搜索"
		} else {
			u.status = strconv.Itoa(len(u.filtered)) + " 匹配 · Esc 关闭"
		}
		return
	}
	u.status = strconv.Itoa(len(u.filtered)) + " shown · " + strconv.Itoa(len(u.programs)) + " total · 按键搜索"
}

func (u *ui) applyFilter() {
	q := strings.TrimSpace(strings.ToLower(u.search.Text()))
	if q == "" {
		u.filtered = append([]catalog.Program(nil), u.programs...)
		return
	}
	out := make([]catalog.Program, 0, len(u.programs))
	for _, p := range u.programs {
		if strings.Contains(strings.ToLower(p.DisplayName), q) ||
			strings.Contains(strings.ToLower(p.Publisher), q) {
			out = append(out, p)
		}
	}
	u.filtered = out
}

func (u *ui) openSearch(initial string) {
	u.closeMenu()
	u.searchOpen = true
	if initial != "" {
		u.search.SetText(initial)
		u.search.SetCaret(len([]rune(initial)), len([]rune(initial)))
	}
	u.applyFilter()
	u.refreshStatus()
	u.wantSearchFocus = true
	u.selected = -1
}

func (u *ui) closeSearch() {
	if !u.searchOpen && u.search.Text() == "" {
		return
	}
	u.searchOpen = false
	u.search.SetText("")
	u.applyFilter()
	u.refreshStatus()
	u.wantKeyFocus = true
}

func (u *ui) ensureRows(n int) {
	for len(u.rows) < n {
		u.rows = append(u.rows, rowState{})
	}
}

func (u *ui) update(gtx layout.Context) {
	if acts := u.deco.Update(gtx); acts != 0 {
		u.win.Perform(acts)
	}
	for {
		ev, ok := u.search.Update(gtx)
		if !ok {
			break
		}
		if _, isChange := ev.(widget.ChangeEvent); isChange {
			if strings.TrimSpace(u.search.Text()) == "" {
				// Cleared by Backspace/Delete — hide search bar.
				if u.searchOpen {
					u.closeSearch()
					gtx.Execute(op.InvalidateCmd{})
				}
				continue
			}
			if !u.searchOpen {
				u.searchOpen = true
			}
			u.applyFilter()
			u.refreshStatus()
			u.selected = -1
			u.closeMenu()
			u.wantSearchFocus = true
		}
	}
	if u.btnSys.Update(gtx) {
		u.includeSys = u.btnSys.Value
		u.reload()
	}
	u.updateMenu(gtx)
	u.updateScanDialog(gtx)
	u.handleKeys(gtx)

	if u.wantSearchFocus {
		gtx.Execute(key.FocusCmd{Tag: &u.search})
		u.wantSearchFocus = false
	} else if u.wantKeyFocus && !u.searchOpen && !u.menu.open {
		gtx.Execute(key.FocusCmd{Tag: &u.keyTag})
		u.wantKeyFocus = false
	}
}

func (u *ui) handleKeys(gtx layout.Context) {
	if u.menu.open || u.scanOpen || u.scanBusy {
		return
	}

	if !u.searchOpen {
		for {
			ev, ok := gtx.Event(key.FocusFilter{Target: &u.keyTag})
			if !ok {
				break
			}
			switch e := ev.(type) {
			case key.EditEvent:
				if e.Text != "" {
					u.openSearch(e.Text)
					gtx.Execute(op.InvalidateCmd{})
				}
			}
		}
		for {
			ev, ok := gtx.Event(key.Filter{Focus: &u.keyTag})
			if !ok {
				break
			}
			e, ok := ev.(key.Event)
			if !ok || e.State != key.Press {
				continue
			}
			if e.Modifiers.Contain(key.ModCtrl) && (e.Name == "F" || e.Name == "f") {
				u.openSearch("")
				gtx.Execute(op.InvalidateCmd{})
				continue
			}
			if e.Name == key.NameF5 {
				u.reload()
				gtx.Execute(op.InvalidateCmd{})
				continue
			}
			if ch, ok := keyToSearchChar(e); ok {
				u.openSearch(ch)
				gtx.Execute(op.InvalidateCmd{})
			}
		}
		return
	}

	for {
		ev, ok := gtx.Event(key.Filter{Focus: &u.search, Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			u.closeSearch()
			gtx.Execute(op.InvalidateCmd{})
		}
	}
}

func keyToSearchChar(e key.Event) (string, bool) {
	if e.Modifiers.Contain(key.ModCtrl) || e.Modifiers.Contain(key.ModAlt) || e.Modifiers.Contain(key.ModSuper) {
		return "", false
	}
	switch e.Name {
	case key.NameEscape, key.NameReturn, key.NameEnter, key.NameTab,
		key.NameLeftArrow, key.NameRightArrow, key.NameUpArrow, key.NameDownArrow,
		key.NameHome, key.NameEnd, key.NamePageUp, key.NamePageDown,
		key.NameDeleteBackward, key.NameDeleteForward,
		key.NameF1, key.NameF2, key.NameF3, key.NameF4, key.NameF5,
		key.NameF6, key.NameF7, key.NameF8, key.NameF9, key.NameF10,
		key.NameF11, key.NameF12,
		key.NameCtrl, key.NameShift, key.NameAlt, key.NameSuper:
		return "", false
	case key.NameSpace:
		return " ", true
	}
	s := string(e.Name)
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 || r == utf8.RuneError || unicode.IsControl(r) {
		return "", false
	}
	// Letter keys are reported uppercase; keep as-is for filtering (case-insensitive).
	if utf8.RuneCountInString(s) != 1 {
		return "", false
	}
	return s, true
}

func (u *ui) current() (catalog.Program, bool) {
	if u.selected < 0 || u.selected >= len(u.filtered) {
		return catalog.Program{}, false
	}
	return u.filtered[u.selected], true
}

func (u *ui) doOpenDir() { u.openDirAt(u.selected) }

func (u *ui) openDirAt(index int) {
	u.closeMenu()
	if index < 0 || index >= len(u.filtered) {
		u.status = "请先选择程序"
		return
	}
	p := u.filtered[index]
	dir := catalog.ResolveInstallDir(p)
	if dir == "" {
		u.status = "未找到安装目录（注册表无 InstallLocation）"
		return
	}
	if err := catalog.OpenFolder(dir); err != nil {
		u.status = "打开目录失败: " + err.Error()
		return
	}
	u.status = "已打开 · " + dir
}

func (u *ui) doOpenReg() { u.openRegAt(u.selected) }

func (u *ui) openRegAt(index int) {
	u.closeMenu()
	if index < 0 || index >= len(u.filtered) {
		u.status = "请先选择程序"
		return
	}
	p := u.filtered[index]
	if p.RegistryKeyPath == "" {
		u.status = "没有可用的注册表路径"
		return
	}
	if err := catalog.OpenRegedit(p.RegistryKeyPath); err != nil {
		u.status = err.Error()
		return
	}
	u.status = "已打开注册表"
}

func (u *ui) doUninstall() { u.uninstallAt(u.selected) }

func (u *ui) uninstallAt(index int) {
	u.closeMenu()
	if u.scanBusy {
		u.status = "正在处理上一次卸载，请稍候"
		return
	}
	if index < 0 || index >= len(u.filtered) {
		u.status = "请先选择要卸载的程序"
		return
	}
	p := u.filtered[index]
	u.scanBusy = true
	u.closeScan()
	u.status = "正在卸载「" + p.DisplayName + "」…完成后自动扫描残留"
	u.win.Invalidate()
	go func(prog catalog.Program) {
		hits, err := catalog.WaitUninstallAndScan(prog)
		u.loadMu.Lock()
		u.scanPend = &scanPending{program: prog, hits: hits, err: err, afterUninstall: true}
		u.loadMu.Unlock()
		u.win.Invalidate()
	}(p)
}

func (u *ui) scanOnlyAt(index int) {
	u.closeMenu()
	if index < 0 || index >= len(u.filtered) {
		u.status = "请先选择程序"
		return
	}
	p := u.filtered[index]
	u.scanBusy = true
	u.status = "正在扫描「" + p.DisplayName + "」残留…"
	u.win.Invalidate()
	go func(prog catalog.Program) {
		hits := catalog.ScanLeftovers(prog)
		u.loadMu.Lock()
		u.scanPend = &scanPending{program: prog, hits: hits, err: nil, afterUninstall: false}
		u.loadMu.Unlock()
		u.win.Invalidate()
	}(p)
}

func (u *ui) openScan(p catalog.Program, hits []catalog.ScanHit) {
	u.scanProg = p
	u.scanRows = make([]scanRow, len(hits))
	for i, h := range hits {
		u.scanRows[i] = scanRow{hit: h}
		u.scanRows[i].check.Value = h.DefaultSelected()
	}
	u.scanOpen = true
	u.scanPhase = "select"
	u.scanDelTotal = 0
	u.scanDelDone = 0
	u.scanDelPath = ""
	u.scanResult = ""
	u.status = "发现 " + strconv.Itoa(len(hits)) + " 项可清理残留 · 请勾选后确认删除"
}

func (u *ui) closeScan() {
	u.scanOpen = false
	u.scanPhase = ""
	u.scanRows = nil
	u.scanDelTotal = 0
	u.scanDelDone = 0
	u.scanDelPath = ""
	u.scanResult = ""
}

func (u *ui) deleteSelectedScan() {
	if u.scanBusy || u.scanPhase != "select" {
		return
	}
	var selected []catalog.ScanHit
	for i := range u.scanRows {
		if u.scanRows[i].check.Value && !u.scanRows[i].hit.Protected {
			selected = append(selected, u.scanRows[i].hit)
		}
	}
	if len(selected) == 0 {
		u.status = "未勾选任何项"
		return
	}
	u.scanBusy = true
	u.scanPhase = "progress"
	u.scanDelTotal = len(selected)
	u.scanDelDone = 0
	u.scanDelPath = selected[0].Path
	u.status = "正在删除残留…"
	u.win.Invalidate()
	go func(items []catalog.ScanHit) {
		var okN, failN int
		for i, h := range items {
			u.loadMu.Lock()
			u.scanDelDone = i
			u.scanDelPath = h.Path
			u.loadMu.Unlock()
			u.win.Invalidate()

			if err := catalog.DeleteHit(h); err != nil {
				failN++
			} else {
				okN++
			}
		}
		msg := "清理完成：成功 " + strconv.Itoa(okN) + " 项"
		if failN > 0 {
			msg += "，失败 " + strconv.Itoa(failN) + " 项（可能需要管理员权限）"
		} else {
			msg += "。请点击确定关闭。"
		}
		u.loadMu.Lock()
		u.scanDelDone = len(items)
		u.scanDelPath = ""
		u.scanResult = msg
		u.scanPhase = "result"
		u.scanBusy = false
		u.status = msg
		u.loadMu.Unlock()
		u.win.Invalidate()
	}(selected)
}

func (u *ui) closeMenu() {
	u.menu.open = false
	u.menu.hover = -1
	u.menu.items = nil
	u.menu.clicks = nil
}

func (u *ui) openMenu(gtx layout.Context, index int, at image.Point) {
	u.selected = index
	u.menu.open = true
	u.menu.index = index
	u.menu.at = at
	u.menu.hover = -1
	idx := index
	u.menu.items = []menuItem{
		{label: "卸载", action: func() { u.uninstallAt(idx) }, danger: true},
		{label: "扫描残留", action: func() { u.scanOnlyAt(idx) }},
		{label: "打开安装目录", action: func() { u.openDirAt(idx) }},
		{label: "打开注册表项", action: func() { u.openRegAt(idx) }},
		{label: "刷新列表", action: u.reload},
	}
	u.menu.clicks = make([]widget.Clickable, len(u.menu.items))
	u.hoverRow = -1
	gtx.Execute(op.InvalidateCmd{})
}

func (u *ui) updateMenu(gtx layout.Context) {
	if !u.menu.open {
		return
	}
	for i := range u.menu.clicks {
		if u.menu.clicks[i].Clicked(gtx) {
			fn := u.menu.items[i].action
			u.closeMenu()
			if fn != nil {
				fn()
			}
			gtx.Execute(op.InvalidateCmd{})
			return
		}
	}
}

func (u *ui) layout(gtx layout.Context) layout.Dimensions {
	paint.Fill(gtx.Ops, colPaper)
	mainGtx := gtx
	if u.scanOpen || u.scanBusy {
		mainGtx = gtx.Disabled()
	}
	u.layoutMain(mainGtx)
	if u.menu.open {
		u.layoutContextMenu(gtx)
	}
	if u.scanOpen {
		u.layoutScanDialog(gtx)
	}
	return layout.Dimensions{Size: gtx.Constraints.Max}
}

func (u *ui) updateScanDialog(gtx layout.Context) {
	if !u.scanOpen {
		return
	}
	switch u.scanPhase {
	case "result":
		if u.btnScanOK.Clicked(gtx) {
			u.closeScan()
			u.reload()
			u.status = "残留清理已确认"
		}
		return
	case "progress":
		return
	case "select":
		// fall through
	default:
		return
	}
	if u.scanBusy {
		return
	}
	for i := range u.scanRows {
		if u.scanRows[i].hit.Protected {
			continue
		}
		if u.scanRows[i].click.Clicked(gtx) {
			u.scanRows[i].check.Value = !u.scanRows[i].check.Value
		}
	}
	if u.btnScanSkip.Clicked(gtx) {
		u.closeScan()
		u.reload()
		u.status = "已跳过残留清理"
		return
	}
	if u.btnScanAll.Clicked(gtx) {
		for i := range u.scanRows {
			if !u.scanRows[i].hit.Protected {
				u.scanRows[i].check.Value = true
			}
		}
	}
	if u.btnScanNone.Clicked(gtx) {
		for i := range u.scanRows {
			u.scanRows[i].check.Value = false
		}
	}
	if u.btnScanDel.Clicked(gtx) {
		u.deleteSelectedScan()
	}
}

func (u *ui) layoutScanDialog(gtx layout.Context) {
	// Dim scrim
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{R: 0x0D, G: 0x10, B: 0x17, A: 0x66})

	panelW := gtx.Dp(620)
	panelH := gtx.Dp(480)
	if u.scanPhase == "progress" || u.scanPhase == "result" {
		panelH = gtx.Dp(220)
	}
	if panelW > gtx.Constraints.Max.X-gtx.Dp(40) {
		panelW = gtx.Constraints.Max.X - gtx.Dp(40)
	}
	if panelH > gtx.Constraints.Max.Y-gtx.Dp(40) {
		panelH = gtx.Constraints.Max.Y - gtx.Dp(40)
	}
	ox := (gtx.Constraints.Max.X - panelW) / 2
	oy := (gtx.Constraints.Max.Y - panelH) / 2
	defer op.Offset(image.Pt(ox, oy)).Push(gtx.Ops).Pop()

	gtx.Constraints.Min = image.Pt(panelW, panelH)
	gtx.Constraints.Max = image.Pt(panelW, panelH)
	defer clip.UniformRRect(image.Rect(0, 0, panelW, panelH), 10).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colPaper)

	layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		switch u.scanPhase {
		case "progress":
			return u.layoutScanProgress(gtx)
		case "result":
			return u.layoutScanResult(gtx)
		default:
			return u.layoutScanSelect(gtx)
		}
	})
}

func (u *ui) layoutScanSelect(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Label(u.theme, unit.Sp(16), "残留清理确认 · "+u.scanProg.DisplayName)
			lbl.Font.Weight = 700
			lbl.Color = colInk
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: 6}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(u.theme, "请勾选要删除的项目。未勾选的不会删除。")
			lbl.Color = colMute
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: 10}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return material.List(u.theme, &u.scanList).Layout(gtx, len(u.scanRows), func(gtx layout.Context, i int) layout.Dimensions {
				return u.layoutScanRow(gtx, i)
			})
		}),
		layout.Rigid(layout.Spacer{Height: 12}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(toolTextBtn(u.theme, &u.btnScanAll, "全选可删项", false)),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(toolTextBtn(u.theme, &u.btnScanNone, "全不选", false)),
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(toolTextBtn(u.theme, &u.btnScanSkip, "跳过", false)),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(toolTextBtn(u.theme, &u.btnScanDel, "删除勾选项", true)),
			)
		}),
	)
}

func (u *ui) layoutScanProgress(gtx layout.Context) layout.Dimensions {
	u.loadMu.Lock()
	done, total, cur := u.scanDelDone, u.scanDelTotal, u.scanDelPath
	u.loadMu.Unlock()
	if total < 1 {
		total = 1
	}
	frac := float32(done) / float32(total)
	if frac > 1 {
		frac = 1
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Label(u.theme, unit.Sp(16), "正在删除残留")
			lbl.Font.Weight = 700
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: 8}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(u.theme, strconv.Itoa(done)+" / "+strconv.Itoa(total)+" · 请稍候，不要关闭窗口")
			lbl.Color = colMute
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: 16}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutProgressBar(gtx, frac)
		}),
		layout.Rigid(layout.Spacer{Height: 12}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			path := cur
			if path == "" {
				path = "准备中…"
			}
			lbl := material.Caption(u.theme, path)
			lbl.Color = colMute
			lbl.MaxLines = 2
			return lbl.Layout(gtx)
		}),
		layout.Flexed(1, layout.Spacer{}.Layout),
	)
}

func (u *ui) layoutScanResult(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Label(u.theme, unit.Sp(16), "清理结果")
			lbl.Font.Weight = 700
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: 12}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			msg := u.scanResult
			if msg == "" {
				msg = "操作已完成"
			}
			lbl := material.Body2(u.theme, msg)
			lbl.Color = colInk
			return lbl.Layout(gtx)
		}),
		layout.Flexed(1, layout.Spacer{}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(toolTextBtn(u.theme, &u.btnScanOK, "确定", true)),
			)
		}),
	)
}

func layoutProgressBar(gtx layout.Context, frac float32) layout.Dimensions {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	h := gtx.Dp(10)
	w := gtx.Constraints.Max.X
	if w < gtx.Dp(120) {
		w = gtx.Dp(120)
	}
	rr := clip.UniformRRect(image.Rect(0, 0, w, h), h/2)
	defer rr.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colSoft)
	fw := int(float32(w) * frac)
	if fw > 0 {
		fill := clip.UniformRRect(image.Rect(0, 0, fw, h), h/2).Push(gtx.Ops)
		paint.Fill(gtx.Ops, colBlue)
		fill.Pop()
	}
	return layout.Dimensions{Size: image.Pt(w, h)}
}

func (u *ui) layoutScanRow(gtx layout.Context, i int) layout.Dimensions {
	row := &u.scanRows[i]
	h := row.hit
	bg := colWhite
	if i%2 == 1 {
		bg = color.NRGBA{R: 0xF5, G: 0xF7, B: 0xFB, A: 0xFF}
	}
	if !h.Protected && row.click.Hovered() {
		bg = colHover
	}
	if !h.Protected && row.check.Value {
		bg = colSelect
	}

	content := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Top: 8, Bottom: 8, Left: 8, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return flatCheckMark(gtx, row.check.Value && !h.Protected, h.Protected)
				}),
				layout.Rigid(layout.Spacer{Width: 10}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(u.theme, h.Path)
							lbl.MaxLines = 1
							lbl.Color = colInk
							return lbl.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							meta := h.Kind.String() + " · " + h.Reason
							if h.SizeLabel() != "—" {
								meta += " · " + h.SizeLabel()
							}
							lbl := material.Caption(u.theme, meta)
							lbl.Color = colMute
							lbl.MaxLines = 1
							return lbl.Layout(gtx)
						}),
					)
				}),
			)
		})
	}

	if h.Protected {
		macro := op.Record(gtx.Ops)
		dims := content(gtx)
		call := macro.Stop()
		area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
		paint.Fill(gtx.Ops, bg)
		call.Add(gtx.Ops)
		area.Pop()
		return dims
	}

	return row.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		macro := op.Record(gtx.Ops)
		dims := content(gtx)
		call := macro.Stop()
		area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
		paint.Fill(gtx.Ops, bg)
		call.Add(gtx.Ops)
		area.Pop()
		return dims
	})
}

func toolTextBtn(th *material.Theme, c *widget.Clickable, label string, primary bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		bg, border, fg := colWhite, colLine, colInk
		if primary {
			bg, border, fg = colInk, colInk, colWhite
		}
		if c.Hovered() && !primary {
			bg = colHover
		}
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return roundedBox(gtx, bg, border, 6, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 8, Bottom: 8, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Label(th, unit.Sp(12), label)
					lbl.Color = fg
					if primary {
						lbl.Font.Weight = 600
					}
					return lbl.Layout(gtx)
				})
			})
		})
	}
}

func (u *ui) layoutMain(gtx layout.Context) {
	if u.menu.open {
		gtx = gtx.Disabled()
		u.hoverRow = -1
		layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(u.layoutTitlebar),
			layout.Flexed(1, u.layoutBody),
			layout.Rigid(u.layoutStatus),
		)
		return
	}

	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, &u.ptrTag)
	if !u.searchOpen {
		key.InputHintOp{Tag: &u.keyTag, Hint: key.HintText}.Add(gtx.Ops)
		event.Op(gtx.Ops, &u.keyTag)
	}
	u.consumePointer(gtx)
	u.hoverRow = -1

	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(u.layoutTitlebar),
		layout.Flexed(1, u.layoutBody),
		layout.Rigid(u.layoutStatus),
	)
	area.Pop()
	pass.Pop()
}

func (u *ui) consumePointer(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &u.ptrTag,
			Kinds:  pointer.Move | pointer.Press | pointer.Drag | pointer.Release,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		u.ptrAt = e.Position.Round()
		if e.Kind == pointer.Press &&
			e.Source == pointer.Mouse &&
			e.Buttons.Contain(pointer.ButtonSecondary) &&
			u.hoverRow >= 0 && u.hoverRow < len(u.filtered) {
			u.openMenu(gtx, u.hoverRow, u.ptrAt)
		}
	}
}

func (u *ui) layoutTitlebar(gtx layout.Context) layout.Dimensions {
	const barH = unit.Dp(36)
	h := gtx.Dp(barH)
	return fill(gtx, colTitleBg, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = h
		gtx.Constraints.Max.Y = h
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = h
				gtx.Constraints.Max.Y = h
				dims := u.deco.LayoutMove(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					gtx.Constraints.Min.Y = h
					gtx.Constraints.Max.Y = h
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Flexed(1, layout.Spacer{}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: 14, Right: 8}.Layout(gtx, u.layoutBrand)
						}),
						layout.Flexed(1, layout.Spacer{}.Layout),
					)
				})
				dims.Size.X = gtx.Constraints.Max.X
				dims.Size.Y = h
				return dims
			}),
			layout.Rigid(u.layoutWinButtons),
		)
	}, true)
}

func (u *ui) layoutBrand(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutAppIcon(gtx, unit.Dp(22))
		}),
		layout.Rigid(layout.Spacer{Width: 10}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Label(u.theme, titleFont, "残影")
			lbl.Font.Weight = 700
			lbl.Color = colInk
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: 8}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Label(u.theme, unit.Sp(11), "TRACE")
			lbl.Font.Weight = 700
			lbl.Color = colBlue
			return lbl.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: 10}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			hh := gtx.Dp(12)
			paint.FillShape(gtx.Ops, colLine, clip.Rect{Max: image.Pt(1, hh)}.Op())
			return layout.Dimensions{Size: image.Pt(1, hh)}
		}),
		layout.Rigid(layout.Spacer{Width: 10}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Label(u.theme, titleFont, "彻底卸载")
			lbl.Color = colMute
			return lbl.Layout(gtx)
		}),
	)
}

func (u *ui) layoutWinButtons(gtx layout.Context) layout.Dimensions {
	type winBtn struct {
		action system.Action
		draw   func(layout.Context, color.NRGBA) layout.Dimensions
	}
	btns := []winBtn{
		{system.ActionMinimize, drawMinIcon},
		{system.ActionMaximize, drawMaxIcon},
		{system.ActionClose, drawCloseIcon},
	}
	if u.deco.Maximized {
		btns[1].draw = drawRestoreIcon
	}
	var size image.Point
	btnW, btnH := gtx.Dp(28), gtx.Dp(36)
	for i := range btns {
		b := btns[i]
		cl := u.deco.Clickable(b.action)
		dims := material.Clickable(gtx, cl, func(gtx layout.Context) layout.Dimensions {
			system.ActionInputOp(b.action).Add(gtx.Ops)
			gtx.Constraints.Min = image.Pt(btnW, btnH)
			fg := colMute
			if cl.Hovered() {
				fg = colInk
			}
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return b.draw(gtx, fg)
			})
		})
		size.X += dims.Size.X
		if dims.Size.Y > size.Y {
			size.Y = dims.Size.Y
		}
		op.Offset(image.Pt(dims.Size.X, 0)).Add(gtx.Ops)
	}
	return layout.Dimensions{Size: image.Pt(size.X, btnH)}
}

func drawMinIcon(gtx layout.Context, fg color.NRGBA) layout.Dimensions {
	size := gtx.Dp(10)
	y := float32(size) * 0.55
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(0, y))
	p.LineTo(f32.Pt(float32(size), y))
	st := clip.Stroke{Path: p.End(), Width: 1.4}.Op().Push(gtx.Ops)
	paint.Fill(gtx.Ops, fg)
	st.Pop()
	return layout.Dimensions{Size: image.Pt(size, size)}
}

func drawMaxIcon(gtx layout.Context, fg color.NRGBA) layout.Dimensions {
	size := gtx.Dp(10)
	m := 1
	r := clip.RRect{Rect: image.Rect(m, m, size-m, size-m)}
	st := clip.Stroke{Path: r.Path(gtx.Ops), Width: 1.4}.Op().Push(gtx.Ops)
	paint.Fill(gtx.Ops, fg)
	st.Pop()
	return layout.Dimensions{Size: image.Pt(size, size)}
}

func drawRestoreIcon(gtx layout.Context, fg color.NRGBA) layout.Dimensions {
	size := gtx.Dp(10)
	r1 := clip.RRect{Rect: image.Rect(3, 1, size-1, size-3)}
	st := clip.Stroke{Path: r1.Path(gtx.Ops), Width: 1.2}.Op().Push(gtx.Ops)
	paint.Fill(gtx.Ops, fg)
	st.Pop()
	r2 := clip.RRect{Rect: image.Rect(1, 3, size-3, size-1)}
	st = clip.Stroke{Path: r2.Path(gtx.Ops), Width: 1.2}.Op().Push(gtx.Ops)
	paint.Fill(gtx.Ops, fg)
	st.Pop()
	return layout.Dimensions{Size: image.Pt(size, size)}
}

func drawCloseIcon(gtx layout.Context, fg color.NRGBA) layout.Dimensions {
	size := gtx.Dp(10)
	s := float32(size)
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(1, 1))
	p.LineTo(f32.Pt(s-1, s-1))
	p.MoveTo(f32.Pt(s-1, 1))
	p.LineTo(f32.Pt(1, s-1))
	st := clip.Stroke{Path: p.End(), Width: 1.4}.Op().Push(gtx.Ops)
	paint.Fill(gtx.Ops, fg)
	st.Pop()
	return layout.Dimensions{Size: image.Pt(size, size)}
}

func (u *ui) layoutSysToggle(gtx layout.Context) layout.Dimensions {
	on := u.btnSys.Value
	fg := colMute
	if on {
		fg = colInk
	}
	if u.btnSys.Hovered() {
		fg = colInk
	}
	return u.btnSys.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return flatCheckMark(gtx, on, false)
			}),
			layout.Rigid(layout.Spacer{Width: 6}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(u.theme, "显示系统组件")
				lbl.Color = fg
				return lbl.Layout(gtx)
			}),
		)
	})
}

// flatCheckMark draws the app's shared checkbox (same as status-bar toggle).
func flatCheckMark(gtx layout.Context, on, locked bool) layout.Dimensions {
	sz := gtx.Dp(13)
	r := image.Rect(0, 0, sz, sz)
	box := clip.Rect(r).Push(gtx.Ops)
	switch {
	case locked:
		paint.Fill(gtx.Ops, colSoft)
		paint.FillShape(gtx.Ops, colLine, clip.Stroke{
			Path:  clip.Rect(r).Path(),
			Width: 1,
		}.Op())
	case on:
		paint.Fill(gtx.Ops, colBlue)
	default:
		paint.Fill(gtx.Ops, colWhite)
		paint.FillShape(gtx.Ops, colLine, clip.Stroke{
			Path:  clip.Rect(r).Path(),
			Width: 1,
		}.Op())
	}
	box.Pop()
	if on && !locked {
		var p clip.Path
		p.Begin(gtx.Ops)
		s := float32(sz)
		p.MoveTo(f32.Pt(s*0.2, s*0.52))
		p.LineTo(f32.Pt(s*0.4, s*0.72))
		p.LineTo(f32.Pt(s*0.8, s*0.28))
		st := clip.Stroke{Path: p.End(), Width: 1.5}.Op().Push(gtx.Ops)
		paint.Fill(gtx.Ops, colWhite)
		st.Pop()
	}
	if locked {
		// small dash to signal non-selectable
		y := sz / 2
		paint.FillShape(gtx.Ops, colMute, clip.Rect{
			Min: image.Pt(gtx.Dp(3), y),
			Max: image.Pt(sz-gtx.Dp(3), y+gtx.Dp(1)),
		}.Op())
	}
	return layout.Dimensions{Size: image.Pt(sz, sz)}
}

func (u *ui) layoutBody(gtx layout.Context) layout.Dimensions {
	return layout.Flex{}.Layout(gtx,
		layout.Flexed(1, u.layoutTable),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(300)
			gtx.Constraints.Max.X = gtx.Dp(300)
			return u.layoutSide(gtx)
		}),
	)
}

func (u *ui) layoutTable(gtx layout.Context) layout.Dimensions {
	u.ensureRows(len(u.filtered))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return fill(gtx, colHeader, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 10, Bottom: 10, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx,
						layout.Flexed(2.2, headerCell(u.theme, "程序")),
						layout.Flexed(1.2, headerCell(u.theme, "发布者")),
						layout.Rigid(fixedHeader(u.theme, "大小", 88)),
						layout.Rigid(fixedHeader(u.theme, "安装日期", 108)),
						layout.Rigid(fixedHeader(u.theme, "版本", 108)),
					)
				})
			}, true)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return material.List(u.theme, &u.list).Layout(gtx, len(u.filtered), func(gtx layout.Context, i int) layout.Dimensions {
				return u.layoutRow(gtx, i)
			})
		}),
	)
}

func (u *ui) layoutRow(gtx layout.Context, i int) layout.Dimensions {
	p := u.filtered[i]
	row := &u.rows[i]

	// When menu is open, paint rows without registering clickables so they
	// cannot steal hover/cursor from the context menu.
	if u.menu.open {
		return u.paintRow(gtx, i, p, false)
	}

	clicked := row.click.Clicked(gtx)
	dims := row.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		hovered := row.click.Hovered()
		if hovered {
			u.hoverRow = i
		}
		return u.paintRow(gtx, i, p, hovered)
	})

	if clicked || row.click.Pressed() {
		if u.selected != i {
			u.selected = i
			u.closeMenu()
			u.status = "已选择 · " + p.DisplayName + " · 按键搜索"
			u.wantKeyFocus = true
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	return dims
}

func (u *ui) paintRow(gtx layout.Context, i int, p catalog.Program, hovered bool) layout.Dimensions {
	selected := i == u.selected
	bg := colWhite
	if i%2 == 1 {
		bg = color.NRGBA{R: 0xF5, G: 0xF7, B: 0xFB, A: 0xFF}
	}
	if hovered && !selected {
		bg = colHover
	}
	if selected {
		bg = colSelect
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	macro := op.Record(gtx.Ops)
	content := layout.Inset{Top: 10, Bottom: 10, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(2.2, cell(u.theme, p.DisplayName, true)),
			layout.Flexed(1.2, cell(u.theme, dash(p.Publisher), false)),
			layout.Rigid(fixedCell(u.theme, p.SizeDisplay(), 88, false)),
			layout.Rigid(fixedCell(u.theme, dash(p.InstallDate), 108, false)),
			layout.Rigid(fixedCell(u.theme, dash(p.Version), 108, false)),
		)
	})
	call := macro.Stop()
	area := clip.Rect{Max: content.Size}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, bg)
	call.Add(gtx.Ops)
	if selected {
		paint.FillShape(gtx.Ops, colBlue, clip.Rect{Max: image.Pt(gtx.Dp(3), content.Size.Y)}.Op())
	}
	area.Pop()
	return content
}

func (u *ui) layoutSide(gtx layout.Context) layout.Dimensions {
	return fill(gtx, colSide, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				paint.FillShape(gtx.Ops, colLine, clip.Rect{Max: image.Pt(1, gtx.Constraints.Max.Y)}.Op())
				return layout.Dimensions{Size: gtx.Constraints.Max}
			}),
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 16, Bottom: 16, Left: 17, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					title, path, flow, reg := "未选择", "—", "标准卸载 → 残留扫描", "—"
					if p, ok := u.current(); ok {
						title = p.DisplayName
						if dir := catalog.ResolveInstallDir(p); dir != "" {
							path = dir
						} else if p.InstallLocation != "" {
							path = p.InstallLocation
						}
						if p.RegistryKeyPath != "" {
							reg = p.RegistryKeyPath
						}
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.H6(u.theme, title)
							lbl.Font.Weight = 600
							return lbl.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Height: 18}.Layout),
						layout.Rigid(sideLabel(u.theme, "安装路径")),
						layout.Rigid(sideValue(u.theme, path)),
						layout.Rigid(layout.Spacer{Height: 14}.Layout),
						layout.Rigid(sideLabel(u.theme, "流程")),
						layout.Rigid(sideValue(u.theme, flow)),
						layout.Rigid(layout.Spacer{Height: 14}.Layout),
						layout.Rigid(sideLabel(u.theme, "注册表")),
						layout.Rigid(sideValue(u.theme, reg)),
						layout.Rigid(layout.Spacer{Height: 12}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Caption(u.theme, "提示：右键卸载后会扫描残留并勾选确认")
							lbl.Color = colBlue
							return lbl.Layout(gtx)
						}),
						layout.Flexed(1, layout.Spacer{}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return roundedBox(gtx, colWhite, colLine, 10, func(gtx layout.Context) layout.Dimensions {
								return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									lbl := material.Caption(u.theme, "删除注册表与临时数据前需勾选确认，避免误删。")
									lbl.Color = colMute
									return lbl.Layout(gtx)
								})
							})
						}),
					)
				})
			}),
		)
	}, false)
}

func (u *ui) layoutStatus(gtx layout.Context) layout.Dimensions {
	return fill(gtx, colHeader, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 8, Bottom: 8, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if u.searchOpen {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Label(u.theme, unit.Sp(11), "搜索")
						lbl.Color = colBlue
						lbl.Font.Weight = 700
						return lbl.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Width: 10}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return roundedBox(gtx, colWhite, colBlue, 6, func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: 6, Bottom: 6, Left: 10, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								ed := material.Editor(u.theme, &u.search, "输入程序名或发布者…")
								ed.Color = colInk
								ed.HintColor = colMute
								ed.TextSize = unit.Sp(13)
								return ed.Layout(gtx)
							})
						})
					}),
					layout.Rigid(layout.Spacer{Width: 10}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Caption(u.theme, u.status)
						lbl.Color = colMute
						return lbl.Layout(gtx)
					}),
				)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Caption(u.theme, u.status)
					lbl.Color = colMute
					return lbl.Layout(gtx)
				}),
				layout.Rigid(u.layoutSysToggle),
			)
		})
	}, false)
}

func (u *ui) layoutContextMenu(gtx layout.Context) {
	if !u.menu.open || len(u.menu.items) == 0 {
		return
	}
	if len(u.menu.clicks) != len(u.menu.items) {
		u.menu.clicks = make([]widget.Clickable, len(u.menu.items))
	}

	menuW := gtx.Dp(200)
	itemH := gtx.Dp(36)
	pad := gtx.Dp(6)
	menuH := pad*2 + itemH*len(u.menu.items)
	pos := u.menu.at
	if pos.X+menuW > gtx.Constraints.Max.X {
		pos.X = gtx.Constraints.Max.X - menuW - gtx.Dp(8)
	}
	if pos.Y+menuH > gtx.Constraints.Max.Y {
		pos.Y = gtx.Constraints.Max.Y - menuH - gtx.Dp(8)
	}
	if pos.X < gtx.Dp(4) {
		pos.X = gtx.Dp(4)
	}
	if pos.Y < gtx.Dp(4) {
		pos.Y = gtx.Dp(4)
	}
	menuRect := image.Rectangle{Min: pos, Max: pos.Add(image.Pt(menuW, menuH))}

	// Fullscreen trap: no PassOp, so hits stop here unless a later (menu)
	// area claims them. Cursor forced to default over the scrim.
	trap := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, &u.menu.tag)
	pointer.CursorDefault.Add(gtx.Ops)
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &u.menu.tag,
			Kinds:  pointer.Press | pointer.Move,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		pt := e.Position.Round()
		if e.Kind == pointer.Press && !pt.In(menuRect) {
			u.closeMenu()
			gtx.Execute(op.InvalidateCmd{})
		}
		if e.Kind == pointer.Move && !pt.In(menuRect) && u.menu.hover != -1 {
			u.menu.hover = -1
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	trap.Pop()

	// Menu panel + items registered AFTER the trap so they win hit-testing.
	off := op.Offset(pos).Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	panelClip := clip.UniformRRect(image.Rect(0, 0, menuW, menuH), 10).Push(gtx.Ops)
	paint.Fill(gtx.Ops, colWhite)
	paint.FillShape(gtx.Ops, colLine, clip.Stroke{
		Path:  clip.UniformRRect(image.Rect(0, 0, menuW, menuH), 10).Path(gtx.Ops),
		Width: 1,
	}.Op())
	event.Op(gtx.Ops, &u.menu)
	pointer.CursorPointer.Add(gtx.Ops)

	// Track pointer over menu for hover index (belt + suspenders vs Clickable).
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &u.menu,
			Kinds:  pointer.Move | pointer.Enter | pointer.Leave | pointer.Press,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		if e.Kind == pointer.Leave {
			if u.menu.hover != -1 {
				u.menu.hover = -1
				gtx.Execute(op.InvalidateCmd{})
			}
			continue
		}
		local := e.Position.Round()
		hover := -1
		if local.Y >= pad && local.Y < menuH-pad {
			hover = (local.Y - pad) / itemH
			if hover < 0 || hover >= len(u.menu.items) {
				hover = -1
			}
		}
		if hover != u.menu.hover {
			u.menu.hover = hover
			gtx.Execute(op.InvalidateCmd{})
		}
	}

	y := pad
	for i := range u.menu.items {
		i := i
		item := u.menu.items[i]
		clk := &u.menu.clicks[i]
		itemOff := op.Offset(image.Pt(pad, y)).Push(gtx.Ops)
		sz := image.Pt(menuW-pad*2, itemH)
		gtxItem := gtx
		gtxItem.Constraints.Min = sz
		gtxItem.Constraints.Max = sz

		hovered := u.menu.hover == i || clk.Hovered()
		_ = clk.Layout(gtxItem, func(gtx layout.Context) layout.Dimensions {
			bg := colWhite
			if hovered {
				bg = colHover
			}
			area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
			paint.Fill(gtx.Ops, bg)
			area.Pop()
			return layout.Inset{Top: 8, Bottom: 8, Left: 10, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				lbl := material.Label(u.theme, toolFont, item.label)
				if item.danger {
					lbl.Color = colDanger
					lbl.Font.Weight = 600
				} else {
					lbl.Color = colInk
				}
				return lbl.Layout(gtx)
			})
		})
		itemOff.Pop()
		y += itemH
	}
	panelClip.Pop()
	pass.Pop()
	off.Pop()
}

func headerCell(th *material.Theme, s string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		lbl := material.Caption(th, s)
		lbl.Color = colMute
		lbl.Font.Weight = 600
		return lbl.Layout(gtx)
	}
}

func fixedHeader(th *material.Theme, s string, dp int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Dp(unit.Dp(dp))
		return headerCell(th, s)(gtx)
	}
}

func cell(th *material.Theme, s string, strong bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		lbl := material.Body2(th, s)
		if strong {
			lbl.Font.Weight = 600
		} else {
			lbl.Color = colMute
		}
		lbl.MaxLines = 1
		return lbl.Layout(gtx)
	}
}

func fixedCell(th *material.Theme, s string, dp int, strong bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Dp(unit.Dp(dp))
		return cell(th, s, strong)(gtx)
	}
}

func sideLabel(th *material.Theme, s string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		lbl := material.Caption(th, s)
		lbl.Color = colMute
		lbl.Font.Weight = 600
		return lbl.Layout(gtx)
	}
}

func sideValue(th *material.Theme, s string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		lbl := material.Body2(th, s)
		lbl.Color = colInk
		return lbl.Layout(gtx)
	}
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func fill(gtx layout.Context, c color.NRGBA, w layout.Widget, borderBottom bool) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()
	area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, c)
	call.Add(gtx.Ops)
	if borderBottom {
		stack := clip.Rect{Min: image.Pt(0, dims.Size.Y-1), Max: dims.Size}.Push(gtx.Ops)
		paint.Fill(gtx.Ops, colLine)
		stack.Pop()
	}
	area.Pop()
	return dims
}

func roundedBox(gtx layout.Context, bg, border color.NRGBA, radius int, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()
	r := image.Rectangle{Max: dims.Size}
	rr := clip.UniformRRect(r, radius)
	defer rr.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, bg)
	paint.FillShape(gtx.Ops, border, clip.Stroke{Path: rr.Path(gtx.Ops), Width: 1}.Op())
	call.Add(gtx.Ops)
	return dims
}
