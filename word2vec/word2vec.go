package word2vec

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"strings"

	"appengine"
	"appengine/blobstore"
	"appengine/user"
)

type Model struct {
	words, size int
	vocab       []string
	M           []float32
}

type wordDistance struct {
	Word     string
	Distance float64
}

const (
	maxSize  int    = 2000
	N        int    = 40
	analogy  string = "analogy"
	distance string = "distance"
)

var (
	model          *Model
	uploadTemplate *template.Template
	indexTemplate  *template.Template
)

func init() {
	var err error
	indexTemplate, err = template.ParseFiles("index.html")
	if err != nil {
		log.Fatal(err)
	}
	uploadTemplate, err = template.ParseFiles("upload.html")
	if err != nil {
		log.Fatal(err)
	}
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/distance", handleDistance)
	http.HandleFunc("/analogy", handleAnalogy)
}

func serveError(c appengine.Context, w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	w.Header().Set("Content-Type", "text/plain")
	io.WriteString(w, "Internal Server Error")
	c.Errorf("%v", err)
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	u := user.Current(c)
	if u == nil {
		url, err := user.LoginURL(c, r.URL.String())
		if err != nil {
			serveError(c, w, err)
			return
		}
		w.Header().Set("Location", url)
		w.WriteHeader(http.StatusFound)
		return
	}
	switch r.Method {
	case "GET":
		uploadURL, err := blobstore.UploadURL(c, "/upload", nil)
		if err != nil {
			serveError(c, w, err)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		err = uploadTemplate.Execute(w, uploadURL)
		if err != nil {
			c.Errorf("%v", err)
		}
	case "POST":
		blobs, _, err := blobstore.ParseUpload(r)
		if err != nil {
			serveError(c, w, err)
			return
		}
		file := blobs["file"]
		if len(file) == 0 {
			c.Errorf("no file uploaded")
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		fmt.Fprintf(w, string(file[0].BlobKey))
	}
}

func readBlob(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	model = new(Model)
	blob := blobstore.NewReader(c, appengine.BlobKey("STOREDBLOBSKEY"))
	reader := bufio.NewReader(blob)

	fmt.Fscanln(reader, &model.words, &model.size)

	var ch string
	model.vocab = make([]string, model.words)
	model.M = make([]float32, model.size*model.words)
	for b := 0; b < model.words; b++ {
		tmp := make([]float32, model.size)
		fmt.Fscanf(reader, "%s%c", &model.vocab[b], &ch)
		bytes, _ := reader.ReadBytes(' ')
		model.vocab[b] = string(bytes[:len(bytes)-1])
		binary.Read(reader, binary.LittleEndian, tmp)
		length := 0.0
		for _, v := range tmp {
			length += float64(v * v)
		}
		length = math.Sqrt(length)
		for i, _ := range tmp {
			tmp[i] /= float32(length)
		}
		copy(model.M[b*model.size:b*model.size+model.size], tmp)
	}
}

func (m *Model) initVector(bi []int, st []string, purpose string) []float32 {
	vec := make([]float32, maxSize)
	switch purpose {
	case distance:
		for b, _ := range st {
			if bi[b] == -1 {
				continue
			}
			for a := 0; a < m.size; a++ {
				vec[a] += m.M[a+bi[b]*m.size]
			}
		}
	case analogy:
		for a := 0; a < m.size; a++ {
			vec[a] = m.M[a+bi[1]*m.size] - m.M[a+bi[0]*m.size] + m.M[a+bi[2]*m.size]
		}

	}
	return vec
}

func (m *Model) distances(st []string, purpose string) []wordDistance {
	bi := make([]int, len(st))
	for k, v := range st {
		var b int
		for b = 0; b < m.words; b++ {
			if m.vocab[b] == v {
				break
			}
		}
		if b == m.words {
			b = -1
		}
		bi[k] = b
	}
	vec := m.initVector(bi, st, purpose)
	leng := 0.0
	for _, a := range vec {
		leng += float64(a * a)
	}
	leng = math.Sqrt(leng)
	for a, _ := range vec {
		vec[a] /= float32(leng)
	}
	bestwd := make([]wordDistance, N)
	for c := 0; c < m.words; c++ {
		if purpose == analogy && (c == bi[0] || c == bi[1] || c == bi[2]) {
			continue
		}
		a := 0
		for _, b := range bi {
			if b == c {
				a = 1
			}
		}
		if a == 1 {
			continue
		}
		dist := 0.0
		for a = 0; a < m.size; a++ {
			dist += float64(vec[a] * m.M[a+c*m.size])
		}
		for a = 0; a < N; a++ {
			if dist > bestwd[a].Distance {
				for d := N - 1; d > a; d-- {
					bestwd[d] = bestwd[d-1]
				}
				bestwd[a] = wordDistance{m.vocab[c], dist}
				break
			}
		}
	}
	return bestwd
}

func handleDistance(w http.ResponseWriter, r *http.Request) {
	word := r.FormValue("word")
	words := strings.Split(word, " ")
	data := model.distances(words, distance)
	fmt.Fprintf(w, "%s", data[0].Word)
}

func handleAnalogy(w http.ResponseWriter, r *http.Request) {
	word := r.FormValue("word")
	words := strings.Split(word, " ")
	if len(words) != 3 {
		fmt.Fprintf(w, "%s", "3 WORDS!")
	} else {
		data := model.distances(words, analogy)
		fmt.Fprintf(w, "%s", data[0].Word)
	}
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if model == nil {
		readBlob(w, r)
	}

	c := appengine.NewContext(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err := indexTemplate.Execute(w, "")
	if err != nil {
		c.Errorf("%v", err)
	}
}
