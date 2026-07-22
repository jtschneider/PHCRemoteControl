package project

import "strings"

var shutterKeywords = []string{
	"roll", "jalousie", "shutter", "blind", "raffstore", "markise",
	"beschattung", "sonnenschutz", "store",
}

var motorizedWindowKeywords = []string{
	"lüftung fenster", "luftung fenster", "dachfenster", "fensterantrieb",
	"roof window", "window opener",
}

var outletKeywords = []string{
	"steckdose", "dose", "schuko", "stecker", "outlet", "socket",
	"receptacle", "plug", "pumpe", "pump", "zirkulation",
}

var panicKeywords = []string{
	"panik", "panic", "alarm", "notfall", "notruf", "emergency", "sos",
	"security", "sicherheit",
}

var presenceSimulationKeywords = []string{
	"anwesenheit", "anwesenheitssimulation", "presence", "presence simulation",
	"vacation", "holiday", "urlaub", "abwesend", "away", "simulation",
}

func matchesKeyword(text string, keywords []string) bool {
	text = strings.ToLower(text)
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}
