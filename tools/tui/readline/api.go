// License: GPLv3 Copyright: 2022, Kovid Goyal, <kovid at kovidgoyal.net>

package readline

import (
	"container/list"
	"fmt"
	"strings"

	"kitty/tools/tui/loop"
	"kitty/tools/wcswidth"
)

var _ = fmt.Print

const ST = "\x1b\\"
const PROMPT_MARK = "\x1b]133;"

type RlInit struct {
	Prompt                  string
	HistoryPath             string
	HistoryCount            int
	ContinuationPrompt      string
	EmptyContinuationPrompt bool
	DontMarkPrompts         bool
}

type Position struct {
	X int
	Y int
}

func (self Position) Less(other Position) bool {
	return self.Y < other.Y || (self.Y == other.Y && self.X < other.X)
}

type Action uint

const (
	ActionNil Action = iota
	ActionBackspace
	ActionDelete
	ActionMoveToStartOfLine
	ActionMoveToEndOfLine
	ActionMoveToStartOfDocument
	ActionMoveToEndOfDocument
	ActionMoveToEndOfWord
	ActionMoveToStartOfWord
	ActionCursorLeft
	ActionCursorRight
	ActionEndInput
	ActionAcceptInput
	ActionCursorUp
	ActionHistoryPreviousOrCursorUp
	ActionCursorDown
	ActionHistoryNextOrCursorDown
	ActionHistoryNext
	ActionHistoryPrevious
	ActionClearScreen
	ActionAddText

	ActionStartKillActions
	ActionKillToEndOfLine
	ActionKillToStartOfLine
	ActionKillNextWord
	ActionKillPreviousWord
	ActionKillPreviousSpaceDelimitedWord
	ActionEndKillActions
)

type kill_ring struct {
	items *list.List
}

func (self *kill_ring) append_to_existing_item(text string) {
	e := self.items.Front()
	if e == nil {
		self.add_new_item(text)
	}
	e.Value = e.Value.(string) + text
}

func (self *kill_ring) add_new_item(text string) {
	if text != "" {
		self.items.PushFront(text)
	}
}

func (self *kill_ring) yank() string {
	e := self.items.Front()
	if e == nil {
		return ""
	}
	return e.Value.(string)
}

func (self *kill_ring) pop_yank() string {
	e := self.items.Front()
	if e == nil {
		return ""
	}
	self.items.MoveToBack(e)
	return self.yank()
}

type Readline struct {
	prompt                  string
	prompt_len              int
	continuation_prompt     string
	continuation_prompt_len int
	mark_prompts            bool
	loop                    *loop.Loop
	history                 *History
	kill_ring               kill_ring

	// The number of lines after the initial line on the screen
	cursor_y     int
	screen_width int
	// Input lines
	lines []string
	// The cursor position in the text
	cursor                 Position
	bracketed_paste_buffer strings.Builder
	last_action            Action
}

func New(loop *loop.Loop, r RlInit) *Readline {
	hc := r.HistoryCount
	if hc == 0 {
		hc = 8192
	}
	ans := &Readline{
		prompt: r.Prompt, prompt_len: wcswidth.Stringwidth(r.Prompt), mark_prompts: !r.DontMarkPrompts,
		loop: loop, lines: []string{""}, history: NewHistory(r.HistoryPath, hc), kill_ring: kill_ring{items: list.New().Init()},
	}
	if r.ContinuationPrompt != "" || !r.EmptyContinuationPrompt {
		ans.continuation_prompt = r.ContinuationPrompt
		if ans.continuation_prompt == "" {
			ans.continuation_prompt = "> "
		}
	}
	ans.continuation_prompt_len = wcswidth.Stringwidth(ans.continuation_prompt)
	if ans.mark_prompts {
		ans.prompt = PROMPT_MARK + "A" + ST + ans.prompt
		ans.continuation_prompt = PROMPT_MARK + "A;k=s" + ST + ans.continuation_prompt
	}
	return ans
}

func (self *Readline) Shutdown() {
	self.history.Shutdown()
}

func (self *Readline) AddHistoryItem(hi HistoryItem) {
	self.history.add_item(hi)
}

func (self *Readline) ResetText() {
	self.lines = []string{""}
	self.cursor = Position{}
	self.cursor_y = 0
	self.last_action = ActionNil
}

func (self *Readline) ChangeLoopAndResetText(lp *loop.Loop) {
	self.loop = lp
	self.ResetText()
}

func (self *Readline) Start() {
	self.loop.SetCursorShape(loop.BAR_CURSOR, true)
	self.loop.StartBracketedPaste()
	self.Redraw()
}

func (self *Readline) End() {
	self.loop.SetCursorShape(loop.BLOCK_CURSOR, true)
	self.loop.EndBracketedPaste()
	self.loop.QueueWriteString("\r\n")
	if self.mark_prompts {
		self.loop.QueueWriteString(PROMPT_MARK + "C" + ST)
	}
}

func MarkOutputStart() string {
	return PROMPT_MARK + "C" + ST
}

func (self *Readline) Redraw() {
	self.loop.StartAtomicUpdate()
	self.RedrawNonAtomic()
	self.loop.EndAtomicUpdate()
}

func (self *Readline) RedrawNonAtomic() {
	self.redraw()
}

func (self *Readline) OnKeyEvent(event *loop.KeyEvent) error {
	err := self.handle_key_event(event)
	if err == ErrCouldNotPerformAction {
		err = nil
		self.loop.Beep()
	}
	return err
}

func (self *Readline) OnText(text string, from_key_event bool, in_bracketed_paste bool) error {
	if in_bracketed_paste {
		self.bracketed_paste_buffer.WriteString(text)
		return nil
	}
	if self.bracketed_paste_buffer.Len() > 0 {
		self.bracketed_paste_buffer.WriteString(text)
		text = self.bracketed_paste_buffer.String()
		self.bracketed_paste_buffer.Reset()
	}
	self.add_text(text)
	return nil
}

func (self *Readline) TextBeforeCursor() string {
	return self.text_upto_cursor_pos()
}

func (self *Readline) TextAfterCursor() string {
	return self.text_after_cursor_pos()
}

func (self *Readline) AllText() string {
	return self.all_text()
}

func (self *Readline) CursorAtEndOfLine() bool {
	return self.cursor.X >= len(self.lines[self.cursor.Y])
}

func (self *Readline) OnResize(old_size loop.ScreenSize, new_size loop.ScreenSize) error {
	self.screen_width = int(new_size.CellWidth)
	if self.screen_width < 1 {
		self.screen_width = 1
	}
	self.Redraw()
	return nil
}