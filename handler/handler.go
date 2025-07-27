package handler

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flashSurvey/survey"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

func Static() http.Handler {
	return http.FileServer(http.FS(staticFS))
}

var (
	Templates = template.Must(template.New("").Funcs(template.FuncMap{
		"inc": func(i int) int { return i + 1 },
		"getIfAvail": func(o []string, i int) string {
			if i < len(o) {
				return o[i]
			}
			return ""
		},
	}).ParseFS(templateFS, "templates/*.html"))
	createTemp       = Templates.Lookup("create.html")
	resultTemp       = Templates.Lookup("result.html")
	voteTemp         = Templates.Lookup("vote.html")
	resultTableTemp  = Templates.Lookup("resultTable.html")
	voteNotifyTemp   = Templates.Lookup("voteNotify.html")
	voteQuestionTemp = Templates.Lookup("voteQuestion.html")
)

func EnsureId(handler http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		userId := getId("uid", writer, request)
		request = request.WithContext(context.WithValue(request.Context(), "id", userId))
		handler(writer, request)
	}
}

func GetUserId(request *http.Request) survey.UserID {
	return survey.UserID(request.Context().Value("id").(string))
}

func GetSurveyId(writer http.ResponseWriter, request *http.Request) survey.SurveyID {
	return survey.SurveyID(getId("sid", writer, request))
}

func getId(key string, writer http.ResponseWriter, request *http.Request) string {
	var id string
	c, err := request.Cookie(key)
	if err == nil {
		id = c.Value
	} else {
		id = randomString()
		c = &http.Cookie{
			Name:  key,
			Value: id,
			Path:  "/", // cookie is valid for all paths
		}
		http.SetCookie(writer, c)
	}
	return id
}

func randomString() string {
	from := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	length := 30
	result := make([]byte, length)
	for i := range result {
		result[i] = from[rand.Intn(len(from))]
	}
	return string(result)
}

type CreateData struct {
	SurveyID survey.SurveyID
	Question survey.SurveyQuestion
	Hidden   bool
	Running  bool
	Error    error
}

func (d CreateData) MaxOptions() int {
	n := len(d.Question.Options) + 2
	if n < 5 {
		return 5
	}
	return n
}

func (d CreateData) URL() string {
	return "?q=" + template.URLQueryEscaper(d.Question.String())
}

func Create(host string, debug bool) http.HandlerFunc {
	log.Println("QR-Host:", host)
	if debug {
		log.Println("Debug mode is enabled")
	}
	return func(writer http.ResponseWriter, request *http.Request) {
		userId := GetUserId(request)

		d := CreateData{
			SurveyID: GetSurveyId(writer, request),
		}

		if request.Method == http.MethodPost {
			err := request.ParseForm()
			if err != nil {
				http.Error(writer, "could not parse form: "+err.Error(), http.StatusBadRequest)
				return
			}
			var o []string
			i := 0
			for {
				name := "option" + strconv.Itoa(i)
				if !request.Form.Has(name) {
					break
				}
				op := strings.TrimSpace(request.FormValue(name))
				if op != "" {
					o = append(o, op)
				}
				i++
			}
			d.Question = survey.SurveyQuestion{
				Title:    request.FormValue("title"),
				Options:  o,
				Multiple: request.FormValue("multiple") == "true",
			}
			if !request.Form.Has("more") {
				if request.Form.Has("create") {
					d.Error = survey.New(host, userId, d.SurveyID, d.Question)
				} else {
					d.Error = survey.Uncover(userId, d.SurveyID, debug)
				}
			}
		}
		if !d.Question.Valid() {
			q := request.URL.Query().Get("q")
			if fromUrl, err := survey.DefinitionFromString(q); err == nil {
				d.Question = fromUrl
			} else {
				if running, ok := survey.GetRunningSurvey(userId, d.SurveyID); ok {
					d.Question = running
				} else {
					d.Question = survey.SurveyQuestion{
						Title:   "",
						Options: []string{"Ja", "Nein"},
					}
				}
			}
		}

		d.Hidden, d.Running = survey.IsHiddenRunning(userId, d.SurveyID)

		err := createTemp.Execute(writer, d)
		if err != nil {
			log.Println(err)
			return
		}
	}
}

type ResultData struct {
	QRCode string        `json:"-"`
	Title  string        `json:"Title"`
	Result template.HTML `json:"Result"`
}

func dataFromResult(result survey.Result) ResultData {
	var b bytes.Buffer
	err := resultTableTemp.Execute(&b, result)
	if err != nil {
		log.Println("could not execute result table template:", err)
	}
	return ResultData{
		QRCode: result.QRCode,
		Title:  template.HTMLEscapeString(result.Title),
		Result: template.HTML(b.String()),
	}
}

func Result(writer http.ResponseWriter, request *http.Request) {
	userId := GetUserId(request)
	surveyId := GetSurveyId(writer, request)
	result := survey.GetResult(userId, surveyId)

	data := dataFromResult(result)

	err := resultTemp.Execute(writer, data)
	if err != nil {
		log.Println(err)
	}
}

func ResultRest(writer http.ResponseWriter, request *http.Request) {
	userId := GetUserId(request)
	surveyId := GetSurveyId(writer, request)
	result := survey.GetResult(userId, surveyId)

	jsonData, err := json.Marshal(dataFromResult(result))
	if err != nil {
		http.Error(writer, "could not marshal result: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writer.Header().Set("Content-Type", "application/json")
	_, err = writer.Write(jsonData)
	if err != nil {
		log.Println(err)
	}
}

func Vote(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	surveyId := survey.SurveyID(query.Get("id"))

	question := survey.GetQuestion(surveyId)
	err := voteTemp.Execute(writer, question)
	if err != nil {
		log.Println(err)
	}
}

func VoteRest(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	surveyId := survey.SurveyID(query.Get("id"))
	isOption := query.Has("o")
	var o []int
	if isOption {
		option := query.Get("o")
		for _, s := range strings.Split(option, ",") {
			oi, err := strconv.Atoi(s)
			if err == nil {
				o = append(o, oi)
			}
		}
	}

	userId := GetUserId(request)
	var err error
	if isOption {
		nStr := query.Get("n")
		var n int
		n, err = strconv.Atoi(nStr)
		if err == nil {
			err = survey.Vote(surveyId, userId, o, n)
		}
		err = voteNotifyTemp.Execute(writer, err)
	} else {
		if survey.HasVoted(surveyId, userId) {
			err = voteNotifyTemp.Execute(writer, errors.New("Es gibt noch keine neue Umfrage!"))
		} else {
			question := survey.GetQuestion(surveyId)
			err = voteQuestionTemp.Execute(writer, question)
		}
	}
	if err != nil {
		log.Println(err)
	}
}
