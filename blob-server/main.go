/*
 * blob_server.go
 *
 * Trivial blob-server in #golang.
 *
 */

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
)

//
//  The handle to our storage method
//
var STORAGE StorageHandler

/**
* Called via GET /alive
 */
func HealthHandler(res http.ResponseWriter, req *http.Request) {
	var (
		status int
		err    error
	)
	defer func() {
		if nil != err {
			http.Error(res, err.Error(), status)
		}
	}()
	fmt.Fprintf(res, "alive")
}

/**
 * Called via GET /blob/XXXXXX
 */
func GetHandler(res http.ResponseWriter, req *http.Request) {
	var (
		status int
		err    error
	)
	defer func() {
		if nil != err {
			http.Error(res, err.Error(), status)
		}
	}()

	//
	// Get the ID which is requested.
	//
	vars := mux.Vars(req)
	id := vars["id"]

	//
	// We're in a chroot() so we shouldn't need to worry
	// about relative paths.  That said the chroot() call
	// will have failed if we were not launched by root, so
	// we need to make sure we avoid directory-traversal attacks.
	//
	r, _ := regexp.Compile("^([a-z0-9]+)$")
	if !r.MatchString(id) {
		status = http.StatusInternalServerError
		fmt.Fprintf(res, "Alphanumeric IDs only.")
		return
	}

	//
	// If the request method was HEAD we don't need to
	// lookup & return n the data, just see if it exists.
	//
	//  We'll terminate early and just return the status-code
	// 200 vs. 404.
	//
	if req.Method == "HEAD" {
		res.Header().Set("Connection", "close")

		if STORAGE.Exists(id) {
		} else {
			res.WriteHeader(http.StatusNotFound)
		}
		return
	}

	//
	// If we reached this point then the request was a GET
	// so we lookup the data, returning it if present.
	//
	data, meta := STORAGE.Get(id)

	//
	// The data was missing..
	//
	if data == nil {
		http.NotFound(res, req)
	} else {

		//
		// The meta-data will be used to populate the HTTP-response
		// headers.
		//
		if meta != nil {
			for k, v := range meta {

				//
				// Special case to set the content-type
				// of the returned value.
				//
				if k == "X-Mime-Type" {
					res.Header().Set(k, v)
					k = "Content-Type"
				}

				//
				// Add the response header.
				//
				res.Header().Set(k, v)
			}
		}
		io.Copy(res, bytes.NewReader(*data))
	}
}

/**
 * Fallback handler, returns 404 for all requests.
 */
func MissingHandler(res http.ResponseWriter, req *http.Request) {
	res.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(res, "404 - content is not hosted here.")
}

/**
 * List the IDs of all blobs we know about.
 */
func ListHandler(res http.ResponseWriter, req *http.Request) {

	var list []string

	list = STORAGE.Existing()

	//
	// If the list is non-empty then build up an array
	// of the names, then send as JSON.
	//
	if len(list) > 0 {
		mapB, _ := json.Marshal(list)
		fmt.Fprintf(res, string(mapB))
	} else {
		fmt.Fprintf(res, "[]")
	}
}

/**
 * Upload a file to to the public-root.
 */
func UploadHandler(res http.ResponseWriter, req *http.Request) {
	var (
		status int
		err    error
	)
	defer func() {
		if nil != err {
			http.Error(res, err.Error(), status)
		}
	}()

	//
	// Get the name of the blob to upload.
	//
	// We've previously chdir() and chroot() to the upload
	// directory, so we don't need to worry about any path
	// issues - providing the user isn't trying a traversal
	// attack.
	//
	vars := mux.Vars(req)
	id := vars["id"]

	//
	// Ensure the ID is entirely alphanumeric, to prevent
	// traversal attacks.
	//
	r, _ := regexp.Compile("^([a-z0-9]+)$")
	if !r.MatchString(id) {
		fmt.Fprintf(res, "Alphanumeric IDs only.")
		status = http.StatusInternalServerError
		return
	}

	//
	// Read the body of the request.
	//
	content, err := ioutil.ReadAll(req.Body)
	if err != nil {
		status = http.StatusInternalServerError
		return
	}

	//
	// If we received any X-headers in our request then save
	// them to our extra-hash.  These will be persisted and
	// restored
	//
	extras := make(map[string]string)

	for header, value := range req.Header {
		if strings.HasPrefix(header, "X-") {
			extras[header] = value[0]
		}
	}

	//
	// Store the body, via our interface.
	//
	result := STORAGE.Store(id, content, extras)
	if result == false {
		status = http.StatusInternalServerError
		return
	}

	//
	// Output the result - horrid.
	//
	//  { "id": "foo",
	//   "size": 1234,
	//   "status": "ok",
	//  }
	//
	out := fmt.Sprintf("{\"id\":\"%s\",\"status\":\"OK\",\"size\":%d}", id, len(content))
	fmt.Fprintf(res, string(out))

}

/**
 * Entry point to our code.
 */
func main() {

	//
	// Parse the command-line arguments.
	//
	host := flag.String("host", "127.0.0.1", "The IP to listen upon")
	port := flag.Int("port", 3001, "The port to bind upon")
	store := flag.String("store", "data", "The location to write the data  to")
	flag.Parse()

	//
	// Create a storage system.
	//
	// At the moment we only have a filesystem-based storage
	// class.  In the future it is possible we'd have more, and we'd
	// choose between them via a command-line flag.
	//
	STORAGE = new(FilesystemStorage)
	STORAGE.Setup(*store)

	//
	// Create a new router and our route-mappings.
	//
	router := mux.NewRouter()
	router.HandleFunc("/alive", HealthHandler).Methods("GET")
	router.HandleFunc("/blob/{id}", GetHandler).Methods("GET")
	router.HandleFunc("/blob/{id}", GetHandler).Methods("HEAD")
	router.HandleFunc("/blob/{id}", UploadHandler).Methods("POST")
	router.HandleFunc("/blobs", ListHandler).Methods("GET")
	router.PathPrefix("/").HandlerFunc(MissingHandler)
	http.Handle("/", router)

	//
	// Launch the server
	//
	fmt.Printf("Launching the server on http://%s:%d\n", *host, *port)
	err := http.ListenAndServe(fmt.Sprintf("%s:%d", *host, *port), nil)
	if err != nil {
		panic(err)
	}
}
