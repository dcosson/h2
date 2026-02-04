package overlay

import "syscall"

// MenuSelect executes the currently selected menu item.
func (o *Overlay) MenuSelect() {
	switch o.MenuIdx {
	case 0:
		o.Input = o.Input[:0]
	case 1:
		o.VT.Output.Write([]byte("\033[2J\033[H"))
		o.RenderScreen()
	case 2:
		o.Quit = true
		o.VT.Cmd.Process.Signal(syscall.SIGTERM)
	}
}

// MenuPrev moves to the previous menu item.
func (o *Overlay) MenuPrev() {
	if len(MenuItems) == 0 {
		return
	}
	if o.MenuIdx == 0 {
		o.MenuIdx = len(MenuItems) - 1
	} else {
		o.MenuIdx--
	}
}

// MenuNext moves to the next menu item.
func (o *Overlay) MenuNext() {
	if len(MenuItems) == 0 {
		return
	}
	if o.MenuIdx == len(MenuItems)-1 {
		o.MenuIdx = 0
	} else {
		o.MenuIdx++
	}
}
