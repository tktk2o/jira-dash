package main

// resizeDetail sizes the preview viewport, which does not draw its own chrome
// and starts 0x0 - without this the pane renders nothing at all. What is
// subtracted is what View wraps it in, and which dimension that is depends on
// where the pane sits: beside the table it is width (the rule dividing the
// panes, the column of air after it, and the gap before it); below it it is
// height (one rule, see bottomPreviewChrome).
//
// Its size comes from tableHeight/bottomPreviewHeight rather than its own
// arithmetic, because the table and the pane share whatever chrome is above
// and below them, and computed separately the two drifted - opening the help
// once pushed two lines off the bottom of the terminal.
//
// Update owns calling this, by comparing chromeLines across a key. Asking each
// transition to remember instead lost `/`, and the footer fell off the bottom of
// the terminal - TestViewNeverDrawsMoreLinesThanTheTerminalHas covers every
// prompt state so that class of miss fails there rather than on screen.
func (m *Model) resizeDetail() {
	if m.previewPosition() != "right" {
		// Full table width, since the pane sits below rather than beside it, and
		// only its own chrome line is taken off vertically.
		m.detail.Width = max(0, m.tableWidth())
		m.detail.Height = max(0, m.bottomPreviewHeight()-bottomPreviewChrome)
		return
	}
	previewWidth := int(float64(m.width) * m.cfg.Defaults.Preview.Width)
	m.detail.Width = max(0, previewWidth-previewChrome)
	m.detail.Height = max(0, m.tableHeight())
}

// previewPosition is where defaults.preview.position resolves to for this
// terminal's width - see PreviewPosition for what "auto" decides between.
func (m Model) previewPosition() string {
	return PreviewPosition(m.cfg.Defaults.Preview.Position, m.width)
}

// previewShown answers whether the preview pane draws at all. A "right" pane
// keeps the config-wins-when-closed, terminal-decides-otherwise rule it always
// had; a "bottom" pane (explicit, or "auto" resolved to it) costs the table no
// horizontal room to stay open, so it only depends on the toggle.
func (m Model) previewShown() bool {
	if m.previewPosition() == "right" {
		return PreviewVisible(m.previewOpen, m.width, m.cfg.Defaults.Preview.Width)
	}
	return m.previewOpen
}

// minTableRows is the shortest the table is left with once a "bottom" preview
// has taken its share of the terminal's height - defaults.preview.heightLines
// is clamped against this so a value larger than the terminal cannot push
// every row, and the footer with them, off the screen.
const minTableRows = 5

// bottomPreviewHeight is how many lines a "bottom" pane costs vertically,
// chrome included, or 0 when the pane is not drawing there at all (closed, or
// resolved to "right" instead).
func (m Model) bottomPreviewHeight() int {
	if m.previewPosition() == "right" || !m.previewShown() {
		return 0
	}
	room := max(0, m.height-fixedChromeLines-m.chromeLines()-minTableRows)
	return bottomPreviewChrome + min(m.cfg.Defaults.Preview.HeightLines, room)
}

// tableWidth is the pane the table gets: the whole terminal, less a "right"
// preview when one is showing. A "bottom" pane takes height, not width, so it
// never shrinks this.
func (m Model) tableWidth() int {
	if !m.previewShown() || m.previewPosition() != "right" {
		return m.width
	}
	return m.width - int(float64(m.width)*m.cfg.Defaults.Preview.Width)
}
