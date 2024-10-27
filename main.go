package main

import (
	"log"
	"math/rand"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xuri/excelize/v2"
)

type exitCode int

const (
	// Exit codes must not change between versions,
	// only new ones can be added
	// This is also why we do not use iota here
	// as this would prevent accidental renumbering
	ok            exitCode = 0
	databaseError exitCode = 1
	loggingError  exitCode = 2
	teaError      exitCode = 3
	internalError exitCode = 4
)

func exit(code exitCode) {
	os.Exit(int(code))
}

type wordDatabase struct {
	pronouns  []string
	verbs     []string
	verbForms [][]string
}

func read_database() wordDatabase {
	table, err := excelize.OpenFile("./words.xlsx")
	if err != nil {
		log.Printf("[FATAL] %v\n", err)
		exit(databaseError)
	}
	defer func() {
		if err := table.Close(); err != nil {
			log.Printf("[FATAL] %v\n", err)
			exit(databaseError)
		}
	}()

	sheets := table.GetSheetList()
	dataSheet := sheets[0]
	rows, err := table.GetRows(dataSheet)
	if err != nil {
		log.Printf("[FATAL] %v\n", err)
		exit(databaseError)
	}
	if len(rows) < 2 {
		log.Println("[FATAL] Table containts less than 2 lines!")
		exit(databaseError)
	}
	var pronouns []string
	verbs := make([]string, len(rows)-1)
	verbForms := make([][]string, len(rows)-1)
	for row_index, row := range rows {
		if row_index == 0 {
			pronouns = make([]string, len(row)-2)
			copy(pronouns, row[2:])
			continue
		}
		verbForms[row_index-1] = make([]string, len(row)-2)
		for col_index, cell := range row {
			if col_index == 1 {
				verbs[row_index-1] = cell
			}
			if col_index >= 2 {
				verbForms[row_index-1][col_index-2] = cell
			}
		}
	}
	return wordDatabase{
		pronouns,
		verbs,
		verbForms,
	}
}

type question struct {
	noun           string
	verb           string
	correct_answer string
}

func (database wordDatabase) getRandomQuestion() question {
	var pronoun_index = rand.Intn(len(database.pronouns))
	var verb_index = rand.Intn(len(database.verbs))
	if len(database.verbForms[verb_index]) <= pronoun_index {
		log.Printf(
			"[WARNING] No database entry for \"%s\" + \"%s\"\n",
			database.pronouns[pronoun_index],
			database.verbs[verb_index],
		)
		return database.getRandomQuestion()
	}
	return question{
		database.pronouns[pronoun_index],
		database.verbs[verb_index],
		database.verbForms[verb_index][pronoun_index],
	}
}

type mode int

const (
	input mode = iota
	validation
)

type model struct {
	database      *wordDatabase
	question      question
	inputField    textinput.Model
	isInAltscreen bool
	mode          mode
	height        int
	width         int
}

func initialModel() model {
	database := read_database()
	question := database.getRandomQuestion()
	inputField := textinput.New()
	inputField.Focus()
	inputField.Prompt = ""
	inputField.Width = 15
	inputField.CharLimit = 30
	return model{
		database:      &database,
		question:      question,
		inputField:    inputField,
		isInAltscreen: true,
		mode:          input,
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func exitNonExistingMode() {
	log.Println("[FATAL] Model is in a non-existing mode")
	exit(internalError)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			log.Println("[INFO] Quitting...")
			return m, tea.Quit
		case "ctrl+a":
			return m.toggleAltScreen()
		}
	}
	switch m.mode {
	case input:
		return m.inputUpdate(msg)
	case validation:
		return m.validateUpdate(msg)
	default:
		exitNonExistingMode()
		return m, nil // unreachable
	}
}

func (m model) inputUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			log.Println("[INFO] Answer submitted")
			m.inputField.Blur() // Removes focus
			m.mode = validation
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.inputField, cmd = m.inputField.Update(msg)
	return m, cmd
}

func (m model) validateUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			log.Println("[INFO] New question requested")
			m.question = m.database.getRandomQuestion()
			m.inputField.Reset()
			m.inputField.Focus() // Removes focus
			m.mode = input
			return m, textinput.Blink
		}
	}
	var cmd tea.Cmd
	m.inputField, cmd = m.inputField.Update(msg)
	return m, cmd
}

func (m *model) toggleAltScreen() (*model, tea.Cmd) {
	m.isInAltscreen = !m.isInAltscreen
	if m.isInAltscreen {
		return m, tea.EnterAltScreen
	} else {
		return m, tea.ExitAltScreen
	}
}

func (m model) validateAnswer() string {
	answerIsCorrect := m.question.correct_answer == strings.TrimSpace(m.inputField.Value())
	if answerIsCorrect {
		return correctAnswerStyle.Italic(true).Render("Correct!")
	} else {
		return wrongAnswerStyle.Render(
			italic("Wrong!") + background.Render(" Correct answer is: ") + bold(m.question.correct_answer),
		)
	}
}

var (
	lightPink1    = lipgloss.ANSIColor(217)
	lightPink3    = lipgloss.ANSIColor(174)
	lightPink4    = lipgloss.ANSIColor(95)
	darkSeaGreen1 = lipgloss.ANSIColor(193)
	darkSeaGreen4 = lipgloss.ANSIColor(65)
	darkSeaGreen2 = lipgloss.ANSIColor(157)
	// Not using ANSI here since first 16 ones could be redefined
	black = lipgloss.Color("#000000")
)

var (
	background    = lipgloss.NewStyle().Background(black)
	promptStyle   = background.Italic(true).Foreground(darkSeaGreen4)
	questionStyle = background.Foreground(darkSeaGreen1).Width(45 - 14)
	helpMsgStyle  = background.Foreground(lightPink4)
	helpKeyStyle  = helpMsgStyle.Bold(true)

	correctAnswerStyle = background.
				AlignHorizontal(lipgloss.Center).
				Width(39).
				Foreground(darkSeaGreen2)
	wrongAnswerStyle = background.
				AlignHorizontal(lipgloss.Center).
				Width(39).
				Foreground(lightPink1)
	boxStyle = background.
			Align(lipgloss.Left, lipgloss.Center).
			PaddingTop(1).
			PaddingBottom(1).
			PaddingLeft(3).
			PaddingRight(3).
			Width(45).
			Height(9).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lightPink4).
			BorderBackground(black)
)

func italic(s string) string {
	return background.Italic(true).Render(s)
}

func bold(s string) string {
	return background.Bold(true).Render(s)
}

type helpEntry struct {
	bindings []string
	action   string
}

var helpSeparator = helpMsgStyle.Render(" • ")

func renderHelpRow(entries []helpEntry) string {
	rendered_entries := make([]string, len(entries))
	for i, entry := range entries {
		rendered_entries[i] = helpKeyStyle.Render(strings.Join(entry.bindings, "/")) +
			helpMsgStyle.Render(" "+entry.action)
	}
	help_row := strings.Join(rendered_entries, helpSeparator)
	return lipgloss.NewStyle().Inline(true).Render(help_row)
}

func (m model) renderQuestion() string {
	prompt_block := lipgloss.JoinVertical(
		lipgloss.Right,
		promptStyle.Render("Form Clue: "),
		promptStyle.Render("Verb: "),
		promptStyle.Render("Verb Form: "),
	)
	question_block := lipgloss.JoinVertical(
		lipgloss.Left,
		questionStyle.Render(m.question.noun),
		questionStyle.Render(m.question.verb),
		questionStyle.Render(m.inputField.View()),
	)
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		prompt_block,
		question_block,
	)
}

var inputHelp = [...]helpEntry{
	{bindings: []string{"enter"}, action: "submit"},
	{bindings: []string{"esc", "q"}, action: "exit"},
}

func (m model) inputView() string {
	return boxStyle.Render(lipgloss.JoinVertical(
		lipgloss.Left,
		m.renderQuestion(),
		"",
		"",
		"",
		renderHelpRow(inputHelp[:]),
	))
}

var validationHelp = [...]helpEntry{
	{bindings: []string{"enter"}, action: "continue"},
	{bindings: []string{"esc", "q"}, action: "exit"},
}

func (m model) validationView() string {
	return boxStyle.Render(lipgloss.JoinVertical(
		lipgloss.Left,
		m.renderQuestion(),
		"",
		m.validateAnswer(),
		"",
		renderHelpRow(validationHelp[:]),
	))
}

func (m model) View() string {
	var content string
	switch m.mode {
	case input:
		content = m.inputView()
	case validation:
		content = m.validationView()
	default:
		exitNonExistingMode()
	}
	if !m.isInAltscreen {
		return content + "\n"
	}
	return lipgloss.NewStyle().
		Align(lipgloss.Center, lipgloss.Center).
		Width(m.width).
		Height(m.height).
		Background(black).
		Render(content)
}

func main() {
	f, err := tea.LogToFile("log", "")
	defer f.Close()
	if err != nil {
		log.Printf("[FATAL] %v\n", err)
		exit(loggingError)
	}

	log.Println("[INFO] Starting app...")
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	log.Println("[INFO] Starting UI loop...")
	if _, err := p.Run(); err != nil {
		log.Printf("[FATAL] Program finished with error:\n%v", err)
		exit(teaError)
	}
	log.Println("[INFO] Finished successfully")
}
