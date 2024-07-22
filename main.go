package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/gob"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"text/template"
	"time"

	"github.com/gin-gonic/gin"
)

const maxUploadSize = 32 * 1024 * 1024 // 32 MiB maximum attachments upload size.

var baseurl = os.Getenv("BASEURL")

// PageInfo is passed to templates as "Page" to provide context.
type PageInfo struct {
	Title string
	Path  string
}

// NewPageInfo uses the current gin.Context request to provide a relative page path. The title parameter names the page.
func NewPageInfo(c *gin.Context, title string) *PageInfo {
	return &PageInfo{Title: title, Path: c.FullPath()}
}

func main() {
	r := gin.Default()
	r.MaxMultipartMemory = maxUploadSize

	initAWS() // Initialize AWS S3 and the s3Actions global.

	// Declare custom functions for templates.
	r.SetFuncMap(template.FuncMap{
		"datestring": func(unix int64) string {
			return time.Unix(unix, 0).Format(time.UnixDate)
		},
	})

	r.Static("/assets", "./assets") // Serve the /assets folder.

	route404 := func(c *gin.Context) {
		r.LoadHTMLFiles("templates/layout.html", "templates/404.html")
		c.HTML(http.StatusOK, "404.html", gin.H{
			"Page": NewPageInfo(c, "404"),
		})
	}
	r.NoRoute(route404) // Unhandled GET requests route to the 404 page.

	// Home / Upload page.
	r.GET("/", func(c *gin.Context) {
		r.LoadHTMLFiles("templates/layout.html", "templates/index.html")
		c.HTML(http.StatusOK, "index.html", gin.H{
			"Page": NewPageInfo(c, ""),
		})
	})

	// Fetch a previously uploaded message and attachments by its SHA-1 hash.
	r.GET("/:hash", func(c *gin.Context) {
		r.LoadHTMLFiles("templates/layout.html", "templates/submission.html")
		// The hash needs to be in lowercase hex, as that's how the hashes are stored in the database.
		hash := strings.ToLower(c.Param("hash"))

		// A GET request to /example could default to this route because it is the closest match.
		// Here, we just reroute them to the 404 page if hash contains the name of an invalid route.
		if !isValidHex(hash) {
			route404(c)
			return
		}

		upload, err := GetUpload(hash) // Fetch the matching row from the database.

		// If the row could not be found or the hash is invalid
		if err != nil {
			if err == ErrHashInvalid {
				respondError(c, http.StatusBadRequest, err)
			} else {
				route404(c)
				log.Printf("failed to fetch page with hash %v: %v", hash, err)
			}
			return
		}

		c.HTML(http.StatusOK, "submission.html", gin.H{
			"Page":   NewPageInfo(c, hash),
			"Upload": upload, // The row is passed to the template.
		})
	})

	// About page.
	r.GET("/about", func(c *gin.Context) {
		r.LoadHTMLFiles("templates/layout.html", "templates/about.html")
		c.HTML(http.StatusOK, "about.html", gin.H{
			"Page": NewPageInfo(c, "About"),
		})
	})

	// Download attachment endpoint.
	r.GET("/download", func(c *gin.Context) {
		hash := c.Query("hash") // Client must request the full hash of the attachment stored on S3.
		if hash == "" {
			respondError(c, http.StatusBadRequest, errors.New(`"hash" argument required`))
		}

		// Download the attachment object from S3 in parallel.
		data, err := s3Actions.DownloadLargeObject(s3Bucket, hash)
		if err != nil || len(data) == 0 {
			route404(c)
			return
		}

		// We have to decode the gob data into a FileObject.
		file := new(FileObject)
		decoder := gob.NewDecoder(bytes.NewReader(data))
		decoder.Decode(file)

		// Set the filename for the attachment.
		c.Writer.Header().Set("Content-Disposition", "attachment; filename="+file.Filename)
		// Serve the attachment to the requesting client.
		http.ServeContent(c.Writer, c.Request, file.Filename, file.Modtime, bytes.NewReader(file.Contents))
	})

	// Submit text and attachments endpoint.
	r.POST("/submit", func(c *gin.Context) {
		// It's easier to upload files using a multipart form in JavaScript.
		form, _ := c.MultipartForm()
		body := form.Value["body"][0]
		fileHeaders := form.File["files"]

		fileNameHashPairs := make([]string, len(fileHeaders)) // Each item will look like "filename/hash" to easily store the pair in the database.
		for i, fileHeader := range fileHeaders {
			fileObject, err := NewFileObject(fileHeader, time.Now())
			if err != nil {
				respondError(c, http.StatusInternalServerError, fmt.Errorf("failed to open file %q: %v", fileHeader.Filename, err))
				return
			}

			// Encode the FileObject into a gob.
			buffer := new(bytes.Buffer)
			encoder := gob.NewEncoder(buffer)
			encoder.Encode(fileObject)

			// Hash the gob to use as the object key on S3 and for retrieving the upload in the database.
			hash := fmt.Sprintf("%x", sha1.Sum(buffer.Bytes()))

			// Upload the file gob to S3 using the hash as the object key.
			_, err = s3Actions.UploadObject(context.TODO(), s3Bucket, hash, buffer.Bytes())
			if err != nil {
				respondError(c, http.StatusInternalServerError, fmt.Errorf("S3 object upload failed: %v", err))
				return
			}

			fileNameHashPairs[i] = fmt.Sprintf("%s/%s", strings.TrimSpace(fileHeader.Filename), hash)
		}

		// Store the upload in the database.
		hash, err := SubmitUpload(body, fileNameHashPairs)
		if err != nil {
			respondError(c, http.StatusConflict, err)
			return
		}

		hash = hash[:10] // Only return the first 10 characters of the hash to shorten the URL.

		c.JSON(http.StatusOK, gin.H{
			"id":       hash,
			"redirect": fmt.Sprintf("%s/%s", baseurl, hash),
			"message":  "Successfully uploaded",
		})
	})

	r.Run() // Start the webserver.
}

func respondError(c *gin.Context, code int, err error) {
	c.JSON(code, gin.H{
		"message": err.Error(),
	})
	log.Println("Error encountered serving request:", err.Error())
	debug.PrintStack()
}
