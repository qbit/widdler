package main

import (
	"embed"
	"encoding/csv"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/webdav"
	"suah.dev/protect"
)

var twFile = "empty-5.1.23.html"

//go:embed empty-5.1.23.html
var tiddly embed.FS

var (
	davDir   string
	listen   string
	auth     bool
	passPath string
	users    map[string]string
)

func init() {
	users = make(map[string]string)
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Fatalln(err)
	}

	flag.StringVar(&davDir, "wikis", dir, "Directory of TiddlyWikis to serve over WebDAV.")
	flag.StringVar(&listen, "http", "127.0.0.1:8080", "Listen on")
	flag.StringVar(&passPath, "htpass", fmt.Sprintf("%s/.htpasswd", dir), "Path to .htpasswd file..")
	flag.BoolVar(&auth, "auth", true, "Enable HTTP Basic Authentication.")
	flag.Parse()

	// These are OpenBSD specific protections used to prevent unnecessary file access.
	_ = protect.Unveil(passPath, "r")
	_ = protect.Unveil(davDir, "rwc")
	_ = protect.Unveil("/etc/ssl/cert.pem", "r")
	_ = protect.Unveil("/etc/resolv.conf", "r")
	_ = protect.Pledge("stdio wpath rpath cpath inet dns")

	_, fErr := os.Stat(passPath)
	if os.IsNotExist(fErr) {
		if auth {
			fmt.Println("No .htpasswd file found!")
			os.Exit(1)
		}
	} else {
		p, err := os.Open(passPath)
		if err != nil {
			log.Fatal(err)
		}
		defer p.Close()

		ht := csv.NewReader(p)
		ht.Comma = ':'
		ht.Comment = '#'
		ht.TrimLeadingSpace = true

		entries, err := ht.ReadAll()
		if err != nil {
			log.Fatal(err)
		}

		for _, parts := range entries {
			users[parts[0]] = parts[1]
		}
	}
}

func authenticate(user string, pass string) bool {
	htpass, exists := users[user]

	if !exists {
		return false
	}

	err := bcrypt.CompareHashAndPassword([]byte(htpass), []byte(pass))
	return err == nil
}

func logger(f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := time.Now()
		fmt.Printf("%s (%s) [%s] \"%s %s\" %03d\n",
			r.RemoteAddr,
			n.Format(time.RFC822Z),
			r.Method,
			r.URL.Path,
			r.Proto,
			r.ContentLength,
		)
		f(w, r)
	}
}

func createEmpty(path string) error {
	_, fErr := os.Stat(path)
	if os.IsNotExist(fErr) {
		log.Printf("creating %q\n", path)
		twData, _ := tiddly.ReadFile(twFile)
		wErr := ioutil.WriteFile(path, []byte(twData), 0600)
		if wErr != nil {
			return wErr
		}
	}
	return nil
}

func main() {
	wdav := &webdav.Handler{
		LockSystem: webdav.NewMemLS(),
		FileSystem: webdav.Dir(davDir),
	}

	idxHandler := http.FileServer(http.Dir(davDir))

	mux := http.NewServeMux()
	mux.HandleFunc("/", logger(func(w http.ResponseWriter, r *http.Request) {

		if strings.Contains(r.URL.Path, ".htpasswd") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if auth {
			user, pass, ok := r.BasicAuth()
			if !(ok && authenticate(user, pass)) {
				w.Header().Set("WWW-Authenticate", `Basic realm="davfs"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		//wdav.Prefix = user

		fp := path.Join(davDir, r.URL.Path)
		/*
			fp := path.Join(davDir, user, r.URL.Path)
			_, dErr := os.Stat(user)
			if os.IsNotExist(dErr) {
				mErr := os.Mkdir(path.Join(davDir, user), 0700)
				if mErr != nil {
					http.Error(w, mErr.Error(), http.StatusInternalServerError)
					return
				}
			}
		*/

		isHTML, err := regexp.Match(`\.html$`, []byte(r.URL.Path))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if isHTML {
			// HTML files will be created or sent back
			err := createEmpty(fp)
			if err != nil {
				log.Println(err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			wdav.ServeHTTP(w, r)
		} else {
			// Everything else is browsable
			idxHandler.ServeHTTP(w, r)
			return
		}
	}))

	s := http.Server{
		Handler: mux,
	}

	lis, err := net.Listen("tcp", listen)
	if err != nil {
		log.Panic(err)
	}

	log.Printf("Listening for HTTP on 'http://%s'", listen)
	log.Panic(s.Serve(lis))
}
