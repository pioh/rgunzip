package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/karrick/godirwalk"
	"github.com/valyala/fasthttp"
)

var root string
var nextQ chan string

func main() {
	nextQ = make(chan string, 24)
	if len(os.Args) < 2 {
		log.Fatalln("unknown command")
	}
	if os.Args[1] == "recv" {
		recv()
	} else if os.Args[1] == "send" {
		send()
	} else {
		log.Fatalln("unknown command")
	}
}

func recv() {
	var err error
	if root, err = filepath.Abs(os.Args[2]); err != nil {
		log.Fatalln(err)
	}
	if err = fasthttp.ListenAndServe(":8997", fastHTTPHandler); err != nil {
		log.Fatalln(err)
	}
}

func fastHTTPHandler(ctx *fasthttp.RequestCtx) {
	base := filepath.Join(root, string(ctx.RequestURI()))
	if err := os.MkdirAll(base, 755); err != nil {
		log.Printf("faieled mkdirall: %v: %+v", base, err)
		ctx.Error("failed", 500)
		return
	}
	log.Println(base)

	ctx.SetStatusCode(201)
}

func send() {
	var err error
	if root, err = filepath.Abs(os.Args[2]); err != nil {
		log.Fatalln(err)
	}

	wg := sync.WaitGroup{}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for next := range nextQ {
				log.Printf("next %v", next)
				if err := sendJob(next); err != nil {
					log.Printf("fail %v: %+v", next, err)
					time.Sleep(time.Second)
				} else {
					log.Printf("done %v", next)
				}

			}
		}()
	}

	err = godirwalk.Walk(root, &godirwalk.Options{
		Callback: func(osPathname string, de *godirwalk.Dirent) error {
			log.Println(osPathname, de.IsDir(), de.IsRegular())
			if !de.IsRegular() {
				return nil
			}
			log.Println(filepath.Ext(osPathname))
			if filepath.Ext(osPathname) == ".zip" {
				nextQ <- fmt.Sprintf("%s %s\n", de.ModeType(), osPathname)
			}
			return nil
		},
		Unsorted: true, // (optional) set true for faster yet non-deterministic enumeration (see godoc)
	})

	close(nextQ)
	wg.Wait()

	if err != nil {
		log.Fatalln(err)
	}
}

func sendJob(next string) error {
	return nil
}
