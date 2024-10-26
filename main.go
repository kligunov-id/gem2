package main

import (
	"log"
	"math/rand"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xuri/excelize/v2"
)

type exitCode int

const (
	// Exit codes must not change between versions,
	// only new ones can be added
	// This is also why we do not use iota here
	ok            exitCode = 0
	databaseError exitCode = 1
	loggingError  exitCode = 2
	teaError      exitCode = 3
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
	return q.noun + " + " + q.verb + " = ?\n>> "
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

type model struct {
	database *wordDatabase
	q        question
}

func initialModel() model {
	database := read_database()
	q := database.getRandomQuestion()
	return model{
		&database,
		q,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			log.Println("[INFO] Quitting...")
			return m, tea.Quit
		default:
			m.q.verb = m.q.verb + msg.String()
		}
	}
	return m, nil
}

func (m model) View() string {
	return m.q.format()
}

func main() {
	f, err := tea.LogToFile("log", "")
	defer f.Close()
	if err != nil {
		log.Printf("[FATAL] %v\n", err)
		exit(loggingError)
	}

	log.Println("[INFO] Starting app...")
	p := tea.NewProgram(initialModel())
	log.Println("[INFO] Starting UI loop...")
	if _, err := p.Run(); err != nil {
		log.Printf("[FATAL] Program finished with error:\n%v", err)
		exit(teaError)
	}
	log.Println("[INFO] Finished successfully")
}
