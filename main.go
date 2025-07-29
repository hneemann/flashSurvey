package main

import (
	"context"
	"flag"
	"flashSurvey/handler"
	"flashSurvey/survey"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

func main() {
	host := flag.String("host", "", "The host which is seen externally.")
	cert := flag.String("cert", "", "certificate pem")
	key := flag.String("key", "", "certificate key")
	timeOutMin := flag.Int("timeout", 30, "timeout in minutes")
	debug := flag.Bool("debug", false, "debug mode")
	port := flag.Int("port", 8080, "port")
	flag.Parse()

	log.Println("QR-Host:", *host)
	if *debug {
		log.Println("Debug mode is enabled")
	}

	surveys := survey.New(*host, *timeOutMin, *debug)

	http.HandleFunc("/", handler.EnsureId(handler.Create(surveys)))
	http.Handle("/static/", Cache(handler.Static(), 300, !*debug))
	http.HandleFunc("/result/", handler.EnsureId(handler.Result(surveys)))
	http.HandleFunc("/resultRest/", handler.EnsureId(handler.ResultRest(surveys)))
	http.HandleFunc("/vote/", handler.EnsureId(handler.Vote(surveys)))
	http.HandleFunc("/voteRest/", handler.EnsureId(handler.VoteRest(surveys)))
	http.HandleFunc("/move/", handler.EnsureId(handler.Move(surveys)))
	http.HandleFunc("/clear/", handler.EnsureId(handler.Clear(surveys)))

	serv := &http.Server{Addr: ":" + strconv.Itoa(*port)}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-c
		log.Print("terminated by signal ", sig.String())

		err := serv.Shutdown(context.Background())
		if err != nil {
			log.Println(err)
		}
		for {
			<-c
		}
	}()

	var err error
	if *cert != "" && *key != "" {
		log.Println("Starting server with TLS")
		err = serv.ListenAndServeTLS(*cert, *key)
	} else {
		log.Println("Starting server without TLS")
		err = serv.ListenAndServe()
	}
	if err != nil {
		log.Println(err)
	}

}

func Cache(parent http.Handler, minutes int, enableCache bool) http.Handler {
	if enableCache {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Add("Cache-Control", "public, max-age="+strconv.Itoa(minutes*60))
			parent.ServeHTTP(writer, request)
		})
	} else {
		log.Println("browser caching disabled")
		return parent
	}
}
