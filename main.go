package main

import (
	"fmt"
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

func (q question) format() string {
	return q.noun + " + " + q.verb + " = ?"
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
}

func initialModel() model {
	database := read_database()
	question := database.getRandomQuestion()
	inputField := textinput.New()
	inputField.Focus()
	return model{
		database:      &database,
		question:      question,
		inputField:    inputField,
		isInAltscreen: true,
		mode:          input,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func exitNonExistingMode() {
	log.Println("[FATAL] Model is in a non-existing mode")
	exit(internalError)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
			return m, nil
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
		return "Correct!"
	} else {
		return fmt.Sprintf("Wrong!\nCorrect answer is %s", m.question.correct_answer)
	}
}

func (m model) inputView() string {
	return lipgloss.JoinVertical(
		lipgloss.Center,
		m.question.format(),
		m.inputField.View(),
		"",
		"enter to submit, esc/q to quit",
	)
}

func (m model) validationView() string {
	return lipgloss.JoinVertical(
		lipgloss.Center,
		m.question.format(),
		m.inputField.View(),
		"",
		m.validateAnswer(),
		"",
		"enter to continue, esc/q to quit",
	)
}

func (m model) View() string {
	switch m.mode {
	case input:
		return m.inputView()
	case validation:
		return m.validationView()
	}
	exitNonExistingMode()
	return "" // unreachable
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
