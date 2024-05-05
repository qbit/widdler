package main

import (
	"crypto/tls"
	"embed"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/webdav"
	"golang.org/x/term"
	"suah.dev/protect"
)

// Landing will be used to fill our landing template
type Landing struct {
	User string
	URL  string
}

const landingPage = `
<h1>Hello{{if .User}} {{.User}}{{end}}! Welcome to widdler!</h1>

<p>To create a new TiddlyWiki html file, simply append an html file name to the URL in the address bar!</p>

<h3>For example:</h3>

<a href="{{.URL}}">{{.URL}}</a>

<p>This will create a new wiki called "<b>wiki.html</b>"</p>

<p>After creating a wiki, this message will be replaced by a list of your wiki files.</p>
`

var (
	twFile = "empty-5.3.3.html"

	//go:embed empty-5.3.3.html
	tiddly embed.FS
	templ  *template.Template
)

type userHandler struct {
	mu   sync.Mutex
	dav  *webdav.Handler
	fs   http.Handler
	name string
}

type userHandlers struct {
	list []userHandler
	mu   sync.RWMutex
}

func (u *userHandlers) find(name string) *userHandler {
	for i := range u.list {
		if u.list[i].name == name {
			return &u.list[i]
		}
	}
	return nil
}

var (
	auth       string
	davDir     string
	fullListen string
	genHtpass  bool
	handlers   userHandlers
	listen     string
	passPath   string
	tlsCert    string
	tlsKey     string
	users      map[string]string
	version    bool
	build      string
)

var pledges = "stdio wpath rpath cpath tty inet dns unveil"

func init() {
	users = make(map[string]string)
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Fatalln(err)
	}

	flag.StringVar(&davDir, "wikis", dir, "Directory of TiddlyWikis to serve over WebDAV.")
	flag.StringVar(&listen, "http", "localhost:8080", "Listen on")
	flag.StringVar(&tlsCert, "tlscert", "", "TLS certificate.")
	flag.StringVar(&tlsKey, "tlskey", "", "TLS key.")
	flag.StringVar(&passPath, "htpass", fmt.Sprintf("%s/.htpasswd", dir), "Path to .htpasswd file..")
	flag.StringVar(&auth, "auth", "basic", "Enable HTTP Basic Authentication.")
	flag.BoolVar(&genHtpass, "gen", false, "Generate a .htpasswd file or add a new entry to an existing file.")
	flag.BoolVar(&version, "v", false, "Show version and exit.")
	flag.Parse()

	// These are OpenBSD specific protections used to prevent unnecessary file access.
	_ = protect.Unveil(passPath, "rwc")
	_ = protect.Unveil(davDir, "rwc")
	_ = protect.Unveil("/etc/ssl/cert.pem", "r")
	_ = protect.Unveil("/etc/resolv.conf", "r")
	_ = protect.Pledge(pledges)

	templ, err = template.New("landing").Parse(landingPage)
	if err != nil {
		log.Fatalln(err)
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
		wErr := os.WriteFile(path, twData, 0600)
		if wErr != nil {
			return wErr
		}
	}
	return nil
}

func prompt(prompt string, secure bool) (string, error) {
	var input string
	fmt.Print(prompt)

	if secure {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return "", err
		}
		input = string(b)
	} else {
		_, err := fmt.Scanln(&input)
		if err != nil {
			return "", err
		}
	}
	return input, nil
}

func addHandler(u, uPath string) {
	handlers.list = append(handlers.list, userHandler{
		name: u,
		dav: &webdav.Handler{
			LockSystem: webdav.NewMemLS(),
			FileSystem: webdav.Dir(uPath),
			Logger: func(r *http.Request, err error) {
				if err != nil {
					log.Print(err)
				}
			},
		},
		fs: http.FileServer(http.Dir(uPath)),
	})
}

func main() {
	if version {
		fmt.Println(build)
		os.Exit(0)
	}
	if genHtpass {
		user, err := prompt("Username: ", false)
		if err != nil {
			log.Fatalln(err)
		}

		pass, err := prompt("Password: ", true)
		if err != nil {
			log.Fatalln(err)
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(pass), 11)
		if err != nil {
			log.Fatalln(err)
		}

		f, err := os.OpenFile(filepath.Clean(passPath), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			log.Fatalln(err)
		}

		if _, err := f.WriteString(fmt.Sprintf("%s:%s\n", user, hash)); err != nil {
			log.Fatalln(err)
		}

		err = f.Close()
		if err != nil {
			log.Fatalln(err)
		}

		fmt.Printf("Added %q to %q\n", user, passPath)

		os.Exit(0)
	}
	pledges, _ = protect.ReducePledges(pledges, "tty")

	// drop to only read on passPath
	_ = protect.Unveil(passPath, "r")
	pledges, _ = protect.ReducePledges(pledges, "unveil")

	_, fErr := os.Stat(passPath)
	if os.IsNotExist(fErr) {
		if auth == "basic" || auth == "header" {
			fmt.Println("No .htpasswd file found!")
			os.Exit(1)
		}
	} else {
		p, err := os.Open(filepath.Clean(passPath))
		if err != nil {
			log.Fatal(err)
		}

		ht := csv.NewReader(p)
		ht.Comma = ':'
		ht.Comment = '#'
		ht.TrimLeadingSpace = true

		entries, err := ht.ReadAll()
		if err != nil {
			log.Fatal(err)
		}

		err = p.Close()
		if err != nil {
			log.Fatal(err)
		}

		for _, parts := range entries {
			users[parts[0]] = parts[1]
		}
	}

	if auth == "basic" || auth == "header" {
		for u := range users {
			uPath := path.Join(davDir, u)
			addHandler(u, uPath)
		}
	} else {
		addHandler("", davDir)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", logger(func(w http.ResponseWriter, r *http.Request) {
		user, pass := "", ""
		var ok bool

		if strings.Contains(r.URL.Path, ".htpasswd") {
			http.NotFound(w, r)
			return
		}

		// Prevent directory traversal
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}

		if auth == "basic" {
			user, pass, ok = r.BasicAuth()
			if !(ok && authenticate(user, pass)) {
				w.Header().Set("WWW-Authenticate", `Basic realm="widdler"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		} else if auth == "header" {
			var prefix = "Auth"
			for name, values := range r.Header {
				if strings.HasPrefix(name, prefix) {
					user = strings.TrimLeft(name, prefix)
					pass = values[0]
					ok = true
					break
				}
			}

			if !(ok && authenticate(user, pass)) {
				w.Header().Set("WWW-Authenticate", `Basic realm="widdler"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		handlers.mu.RLock()
		handler := handlers.find(user)
		handlers.mu.RUnlock()

		if handler == nil {
			http.NotFound(w, r)
			return
		}

		handler.mu.Lock()

		defer handler.mu.Unlock()

		userPath := path.Join(davDir, user)
		fullPath := path.Join(davDir, user, r.URL.Path)

		_, dErr := os.Stat(userPath)
		if os.IsNotExist(dErr) {
			mErr := os.Mkdir(userPath, 0700)
			if mErr != nil {
				http.Error(w, mErr.Error(), http.StatusInternalServerError)
				return
			}
		}

		isHTML, err := regexp.Match(`\.html$`, []byte(r.URL.Path))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if isHTML {
			// HTML files will be created or sent back
			err := createEmpty(fullPath)
			if err != nil {
				log.Println(err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			handler.dav.ServeHTTP(w, r)
		} else {
			// Everything else is browsable
			entries, err := os.ReadDir(userPath)
			if err != nil {
				log.Println(err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if len(entries) > 0 {
				if r.URL.Path == "/" {
					// If we have entries, and are serving up /, check for
					// index.html and redirect to that if it exists. We redirect
					// because net/http handles index.html magically for FileServer
					_, fErr := os.Stat(filepath.Clean(path.Join(userPath, "index.html")))
					if !os.IsNotExist(fErr) {
						http.Redirect(w, r, "/index.html", http.StatusMovedPermanently)
						return
					}
				}
				handler.fs.ServeHTTP(w, r)
			} else {
				l := Landing{
					URL: fmt.Sprintf("%s/wiki.html", fullListen),
				}
				if user != "" {
					l.User = user
				}
				err = templ.ExecuteTemplate(w, "landing", l)
				if err != nil {
					log.Println(err)
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			}
		}
	}))

	s := http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 0,
	}

	lis, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatalln(err)
	}

	if tlsCert != "" && tlsKey != "" {
		fullListen = fmt.Sprintf("https://%s", listen)

		s.TLSConfig = &tls.Config{
			MinVersion:               tls.VersionTLS12,
			CurvePreferences:         []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
			PreferServerCipherSuites: true,
		}

		log.Printf("Listening for HTTPS on 'https://%s'", listen)
		log.Fatalln(s.ServeTLS(lis, tlsCert, tlsKey))
	} else {
		fullListen = fmt.Sprintf("http://%s", listen)

		log.Printf("Listening for HTTP on 'http://%s'", listen)
		log.Fatalln(s.Serve(lis))
	}
}
