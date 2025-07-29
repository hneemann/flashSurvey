package survey

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/skip2/go-qrcode"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxStringLen = 100
)

type Option struct {
	Title string
	Votes int
}

type Options []Option

type OptionResult struct {
	Title   string
	votes   int
	percent float64
}

func (o OptionResult) PercentVal(max float64) float64 {
	if o.votes < 0 {
		return 0
	}
	return o.percent / max * 100
}

func (o OptionResult) PercentValRemain(max float64) float64 {
	if o.votes < 0 {
		return 100
	}
	return 100 - o.percent/max*100
}

func (o OptionResult) Percent() string {
	if o.votes < 0 {
		return "-"
	}
	return strconv.FormatFloat(o.percent, 'f', 1, 64)
}

func (o OptionResult) Votes() string {
	if o.votes < 0 {
		return "-"
	}
	return strconv.Itoa(o.votes)
}

func (o OptionResult) String() string {
	return fmt.Sprintf("%s: %d (%.1f%%)", o.Title, o.votes, o.percent)
}

func (o Options) result(sum int, hidden bool) ([]OptionResult, float64) {
	if sum <= 0 {
		sum = 1
	}

	var maxPercent float64

	var res []OptionResult
	if hidden {
		for _, option := range o {
			res = append(res, OptionResult{
				Title:   option.Title,
				votes:   -1,
				percent: 0,
			})
			maxPercent = 100.0
		}
	} else {
		for _, option := range o {
			percent := float64(option.Votes) / float64(sum) * 100
			res = append(res, OptionResult{
				Title:   option.Title,
				votes:   option.Votes,
				percent: percent,
			})
			if percent > maxPercent {
				maxPercent = percent
			}
		}
		if maxPercent < 1 {
			maxPercent = 1.0
		}
	}
	return res, maxPercent
}

type SurveyID string
type UserID string

type Survey struct {
	mutex         sync.Mutex
	question      SurveyQuestion
	surveyID      SurveyID
	userID        UserID
	qrCode        string
	options       Options
	number        int
	votesCounted  map[UserID]struct{}
	resultHidden  bool
	creationTime  time.Time
	version       int
	changedNotify chan struct{}
}

func (s *Survey) Lock() {
	s.mutex.Lock()
}

func (s *Survey) Unlock() {
	s.mutex.Unlock()
}

func (s *Survey) changed() {
	s.version++
	if s.changedNotify != nil {
		close(s.changedNotify)
	}
	s.changedNotify = make(chan struct{})
}

type Result struct {
	Title      string
	QRCode     string
	Votes      int
	Result     []OptionResult
	MaxPercent float64
	Version    int
}

func (s *Survey) Result() Result {
	result, maxPercent := s.options.result(len(s.votesCounted), s.resultHidden)
	return Result{
		Title:      s.question.Title,
		QRCode:     s.qrCode,
		Votes:      len(s.votesCounted),
		MaxPercent: maxPercent,
		Result:     result,
		Version:    s.version,
	}
}

type Question struct {
	Number   int
	SurveyID SurveyID
	Question SurveyQuestion
}

func (s *Survey) Question() Question {
	return Question{
		Number:   s.number,
		SurveyID: s.surveyID,
		Question: s.question,
	}
}

type Surveys struct {
	mutex   sync.RWMutex
	surveys map[SurveyID]*Survey
	host    string
	debug   bool
}

var closedChannel chan struct{}

func init() {
	closedChannel = make(chan struct{})
	close(closedChannel)
}

func New(host string, timeoutMin int, debug bool) *Surveys {
	s := &Surveys{
		surveys: make(map[SurveyID]*Survey),
		host:    host,
		debug:   debug,
	}
	s.startSurveyTimeoutCheck(timeoutMin)
	return s
}

type SurveyQuestion struct {
	Title    string
	Options  []string
	Multiple bool
}

func (d SurveyQuestion) Valid() bool {
	return d.Title != "" && len(d.Options) >= 2
}

func (d SurveyQuestion) String() string {
	str := d.clean(d.Title)
	if d.Multiple {
		str += ";m"
	} else {
		str += ";s"
	}
	for _, o := range d.Options {
		if o != "" {
			str += ";" + d.clean(o)
		}
	}
	return str
}

func (d SurveyQuestion) clean(o string) string {
	o = strings.TrimSpace(o)
	o = strings.ReplaceAll(o, ";", "")
	return o
}

func DefinitionFromString(str string) (SurveyQuestion, error) {
	parts := strings.Split(str, ";")
	if len(parts) < 4 {
		return SurveyQuestion{}, errors.New("Ungültige Umfrage-Definition!")
	}

	def := SurveyQuestion{
		Title:    parts[0],
		Multiple: parts[1] == "m",
	}

	for _, option := range parts[2:] {
		option = strings.TrimSpace(option)
		if option != "" {
			def.Options = append(def.Options, option)
		}
	}

	if !def.Valid() {
		return SurveyQuestion{}, errors.New("Ungültige Umfrage-Definition!")
	}

	return def, nil
}

func (s *Surveys) New(userID UserID, surveyID SurveyID, def SurveyQuestion) error {
	opt := make([]Option, len(def.Options))
	for i, option := range def.Options {
		option = strings.TrimSpace(option)
		if option == "" {
			return fmt.Errorf("Option %d ist leer!", i+1)
		} else if len(option) > maxStringLen {
			return fmt.Errorf("Option %d ist zu lang! Maximal %d Zeichen erlaubt.", i+1, maxStringLen)
		}
		opt[i] = Option{Title: option, Votes: 0}
	}

	url := s.host + "/vote/?id=" + string(surveyID)

	qrCode, err := qrcode.Encode(url, qrcode.Medium, 512)
	if err != nil {
		return fmt.Errorf("could not create qr code: %w", err)
	}

	def.Title = strings.TrimSpace(def.Title)
	if def.Title == "" {
		return errors.New("Es fehlt der Titel!")
	} else if len(def.Title) > maxStringLen {
		return fmt.Errorf("Der Titel ist zu lang! Maximal %d Zeichen erlaubt.", maxStringLen)
	}

	if len(opt) < 2 {
		return errors.New("Es müssen mindestens zwei Optionen angegeben werden!")
	}

	survey := &Survey{
		surveyID:     surveyID,
		userID:       userID,
		qrCode:       base64.StdEncoding.EncodeToString(qrCode),
		options:      opt,
		question:     def,
		resultHidden: true,
		votesCounted: make(map[UserID]struct{}),
		creationTime: time.Now(),
		version:      1,
	}

	replaced, surveyCount, err := s.createSurvey(survey)
	if err != nil {
		return err
	}

	if replaced {
		log.Printf("replaced survey with %d options, in total %d surveys", len(opt), surveyCount)
	} else {
		log.Printf("created survey with %d options, in total %d surveys", len(opt), surveyCount)
	}

	return nil
}

func (s *Surveys) createSurvey(newSurvey *Survey) (bool, int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	replaced := false
	if existingSurvey, exists := s.surveys[newSurvey.surveyID]; exists {
		if existingSurvey.userID != newSurvey.userID {
			return false, 0, errors.New("Diese Umfrage existiert bereits und wurde von einem anderen Benutzer erstellt!")
		}
		replaced = true
		newSurvey.number = existingSurvey.number + 1
		newSurvey.version = existingSurvey.version
		newSurvey.changedNotify = existingSurvey.changedNotify
		newSurvey.changed()
	} else {
		newSurvey.changedNotify = make(chan struct{})
	}
	s.surveys[newSurvey.surveyID] = newSurvey
	return replaced, len(s.surveys), nil
}

func (s *Surveys) getSurveyToVote(surveyID SurveyID) (*Survey, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	survey, exists := s.surveys[surveyID]
	return survey, exists
}

func (s *Surveys) getSurveyCheckUser(userId UserID, surveyID SurveyID) (*Survey, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	survey, exists := s.surveys[surveyID]
	if !exists {
		return nil, false
	}
	if survey.userID != userId {
		return nil, false
	}
	return survey, exists
}

func (s *Surveys) GiveAwayQRCode(surveyID SurveyID, userID UserID) (string, error) {
	survey, exists := s.getSurveyCheckUser(userID, surveyID)
	if !exists {
		return "", errors.New("Diese Umfrage existiert nicht!")
	}

	survey.Lock()
	defer survey.Unlock()

	if survey.userID != userID {
		return "", errors.New("Sie sind nicht der Ersteller dieser Umfrage!")
	}

	url := fmt.Sprintf("%s/?tuid=%s&tsid=%s", s.host, userID, surveyID)

	qrCode, err := qrcode.Encode(url, qrcode.Medium, 512)
	if err != nil {
		return "", fmt.Errorf("could not create qr code: %w", err)
	}

	return base64.StdEncoding.EncodeToString(qrCode), nil
}

func (s *Surveys) Uncover(userid UserID, surveyID SurveyID) error {
	survey, exists := s.getSurveyCheckUser(userid, surveyID)
	if !exists {
		return errors.New("Diese Umfrage existiert nicht!")
	}

	survey.Lock()
	defer survey.Unlock()

	if survey.userID != userid {
		return errors.New("Sie sind nicht der Ersteller dieser Umfrage!")
	}

	votes := len(survey.votesCounted)
	if !s.debug && votes > 0 && votes <= 2 {
		return errors.New("Es sind noch nicht genug Stimmen abgegeben worden!")
	}

	survey.resultHidden = false
	survey.changed()
	return nil
}

func (s *Surveys) WaitForModification(userId UserID, surveyId SurveyID, clientVersion int) chan struct{} {
	survey, exists := s.getSurveyCheckUser(userId, surveyId)
	if !exists {
		return nil
	}

	survey.Lock()
	defer survey.Unlock()

	if survey.version > clientVersion {
		// immediate notification if the version is higher than the client's version
		return closedChannel
	}

	return survey.changedNotify
}

func (s *Surveys) GetResult(userId UserID, surveyID SurveyID) Result {
	survey, exists := s.getSurveyCheckUser(userId, surveyID)
	if !exists {
		return Result{Title: "Die Umfrage existiert nicht!"}
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.Result()
}

func (s *Surveys) GetRunningSurvey(userID UserID, surveyID SurveyID) (SurveyQuestion, bool) {
	survey, exists := s.getSurveyCheckUser(userID, surveyID)
	if !exists {
		return SurveyQuestion{}, false
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.question, true
}

func (s *Surveys) IsHiddenRunning(userID UserID, surveyID SurveyID) (bool, bool) {
	survey, exists := s.getSurveyCheckUser(userID, surveyID)
	if !exists {
		return false, false
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.resultHidden, true
}

func (s *Surveys) Vote(surveyID SurveyID, voterId UserID, option []int, number int) error {
	survey, exists := s.getSurveyToVote(surveyID)
	if !exists {
		return errors.New("Diese Umfrage existiert nicht!")
	}

	survey.Lock()
	defer survey.Unlock()

	if number != survey.number {
		return errors.New("Diese Wahl war schon abgeschlossen!")
	}

	if _, voted := survey.votesCounted[voterId]; voted {
		return errors.New("Sie haben bereits abgestimmt!")
	}

	for _, opt := range option {
		if opt < 0 || opt >= len(survey.options) {
			return errors.New("Ungültige Option!")
		}
		survey.options[opt].Votes++
	}

	survey.votesCounted[voterId] = struct{}{}

	survey.changed()

	return nil
}

func (s *Surveys) HasVoted(surveyID SurveyID, voterId UserID) bool {
	survey, exists := s.getSurveyToVote(surveyID)
	if !exists {
		return false
	}

	survey.Lock()
	defer survey.Unlock()

	_, voted := survey.votesCounted[voterId]
	return voted
}

func (s *Surveys) GetQuestion(surveyID SurveyID) Question {
	survey, exists := s.getSurveyToVote(surveyID)
	if !exists {
		return Question{Question: SurveyQuestion{Title: "Die Umfrage existiert nicht!"}}
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.Question()
}

func (s *Surveys) startSurveyTimeoutCheck(timeOutInMin int) {
	surveyTimeout := time.Duration(timeOutInMin) * time.Minute
	go func() {
		log.Println("Starting survey cleanup routine, timeout", surveyTimeout)
		for {
			time.Sleep(surveyTimeout / 2)
			deleted, remaining := s.cleanup(surveyTimeout)
			if deleted > 0 {
				log.Printf("Deleted %d old surveys, %d surveys remaining\n", deleted, remaining)
			}
		}
	}()
}

func (s *Surveys) cleanup(surveyTimeout time.Duration) (int, int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	deleteCount := 0
	for id, survey := range s.surveys {
		if time.Since(survey.creationTime) > surveyTimeout {
			delete(s.surveys, id)
			deleteCount++
		}
	}

	return deleteCount, len(s.surveys)
}
