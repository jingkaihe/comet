package server

// oscTitleParser scans PTY output for OSC 0/2 set-title sequences, keeping
// state across chunk boundaries with bounded buffers.
type oscTitleParser struct {
	state   oscTitleParseState
	param   []byte
	title   []byte
	capture bool
}

type oscTitleParseState int

const (
	oscTitleStateGround oscTitleParseState = iota
	oscTitleStateEscape
	oscTitleStateParam
	oscTitleStateBody
	oscTitleStateBodyEscape
)

const (
	oscTitleParamLimit = 8
	oscTitleBodyLimit  = 512
	asciiBEL           = 0x07
	asciiESC           = 0x1b
	asciiDEL           = 0x7f
)

// feed reports the last complete title in the chunk; "" with found=true
// means the title was cleared.
func (p *oscTitleParser) feed(chunk []byte) (string, bool) {
	title := ""
	found := false
	for _, b := range chunk {
		if next, ok := p.consume(b); ok {
			title = next
			found = true
		}
	}
	return title, found
}

func (p *oscTitleParser) consume(b byte) (string, bool) {
	switch p.state {
	case oscTitleStateGround:
		if b == asciiESC {
			p.state = oscTitleStateEscape
		}
	case oscTitleStateEscape:
		switch b {
		case ']':
			p.state = oscTitleStateParam
			p.param = p.param[:0]
		case asciiESC:
		default:
			p.state = oscTitleStateGround
		}
	case oscTitleStateParam:
		switch {
		case b >= '0' && b <= '9' && len(p.param) < oscTitleParamLimit:
			p.param = append(p.param, b)
		case b == ';':
			param := string(p.param)
			p.beginBody(param == "0" || param == "2")
		case b == asciiBEL:
			p.state = oscTitleStateGround
		case b == asciiESC:
			p.state = oscTitleStateEscape
		default:
			p.beginBody(false)
		}
	case oscTitleStateBody:
		switch {
		case b == asciiBEL:
			return p.commit()
		case b == asciiESC:
			p.state = oscTitleStateBodyEscape
		case p.capture && b >= 0x20 && b != asciiDEL && len(p.title) < oscTitleBodyLimit:
			p.title = append(p.title, b)
		}
	case oscTitleStateBodyEscape:
		if b == '\\' {
			return p.commit()
		}
		// bare ESC aborts the OSC and may start a new sequence
		p.state = oscTitleStateEscape
		return p.consume(b)
	}
	return "", false
}

// beginBody consumes an OSC payload until BEL or ST; non-title sequences
// (OSC 8, 52, ...) run with capture disabled.
func (p *oscTitleParser) beginBody(capture bool) {
	p.state = oscTitleStateBody
	p.title = p.title[:0]
	p.capture = capture
}

func (p *oscTitleParser) commit() (string, bool) {
	captured := p.capture
	title := string(p.title)
	p.beginBody(false)
	p.state = oscTitleStateGround
	if !captured {
		return "", false
	}
	return title, true
}

func composeDisplayTitle(oscTitle, foregroundCommand, displayCWD string) string {
	if oscTitle != "" {
		return oscTitle
	}
	if foregroundCommand != "" {
		return foregroundCommand
	}
	return displayCWD
}
