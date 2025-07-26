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

var (
	Templates = template.Must(template.New("").Funcs(template.FuncMap{
		"inc": func(i int) int { return i + 1 },
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
			Name:     key,
			Value:    id,
			HttpOnly: true,                    // XSS protection, no access from JavaScript
			Secure:   true,                    // only send cookie over HTTPS
			SameSite: http.SameSiteStrictMode, // protect from CSRF
			Path:     "/",                     // cookie is valid for all paths
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
	Title    string
	Multiple bool
	Hidden   bool
	Running  bool
	Options  []string
	Error    error
}

func (d CreateData) URL() string {
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
	return "?q=" + template.URLQueryEscaper(str)
}

func (d CreateData) clean(o string) string {
	o = strings.TrimSpace(o)
	o = strings.ReplaceAll(o, ";", "")
	return o
}

func Create(host string, maxOptions int, debug bool) http.HandlerFunc {
	log.Println("QR-Host:", host)
	if debug {
		log.Println("Debug mode is enabled")
	}
	return func(writer http.ResponseWriter, request *http.Request) {
		userId := GetUserId(request)

		var d CreateData

		q := request.URL.Query().Get("q")
		str := strings.Split(q, ";")
		if len(str) > 3 {
			o := make([]string, maxOptions)
			for i, s := range str[2:] {
				if len(s) > 0 {
					if i < len(o) {
						o[i] = s
					} else {
						break
					}
				}
			}
			d = CreateData{
				Title:    str[0],
				Multiple: str[1] == "m",
				Options:  o,
			}
		} else {
			o := make([]string, maxOptions)
			o[0] = "habe ich nicht einmal verstanden!"
			o[1] = "konnte ich nicht lösen!"
			o[2] = "konnte ich lösen, bin aber nicht fertig geworden!"
			o[3] = "war Ok!"
			o[4] = "war zu leicht!"
			d = CreateData{
				Title:   "Die letzte Aufgabe",
				Options: o,
			}
		}
		d.SurveyID = GetSurveyId(writer, request)

		if request.Method == http.MethodPost {
			err := request.ParseForm()
			if err != nil {
				http.Error(writer, "could not parse form: "+err.Error(), http.StatusBadRequest)
				return
			}
			title := request.FormValue("title")
			d.Title = title
			var options []string
			for i := range maxOptions {
				o := request.FormValue("option" + strconv.Itoa(i))
				if o != "" {
					options = append(options, o)
				}
				d.Options[i] = o
			}
			multiple := request.FormValue("multiple") == "true"
			d.Multiple = multiple

			if request.Form.Has("create") {
				d.Error = survey.New(host, userId, d.SurveyID, title, multiple, options...)
			} else {
				d.Error = survey.Uncover(userId, d.SurveyID, debug)
			}
		}

		d.Hidden, d.Running = survey.IsHiddenRunning(d.SurveyID)

		err := createTemp.Execute(writer, d)
		if err != nil {
			http.Error(writer, "could not execute template: "+err.Error(), http.StatusInternalServerError)
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
	option := query.Get("o")
	var o []int
	for _, s := range strings.Split(option, ",") {
		oi, err := strconv.Atoi(s)
		if err == nil {
			o = append(o, oi)
		}
	}

	userId := GetUserId(request)
	var err error
	if len(o) > 0 {
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
