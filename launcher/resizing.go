package main

import (
	tea "github.com/charmbracelet/bubbletea"
)

type ResizeWrapper struct {
	tea.Model
}

func (r ResizeWrapper) View() string {
	return r.Model.View()
}

func (r ResizeWrapper) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	newModel, cmd := r.Model.Update(msg)
	r.Model = newModel
	switch msg.(type) {
	case tea.WindowSizeMsg:
		break
	default:
		cmd = tea.Batch(cmd, tea.WindowSize())
	}
	return r, cmd
}

func (r ResizeWrapper) Init() tea.Cmd {
	return tea.Batch(r.Model.Init(), tea.WindowSize())
}
