package main

import (
	"bytes"
	"crypto/sha1"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/lpernett/godotenv"
)

var db *sql.DB

var (
	ErrConstraintUnique = errors.New("a field failed the UNIQUE constraint")
	ErrHashInvalid      = errors.New("hash is not valid hex or has a length less than 10 or greater than 40")
)

// The UploadModel represents a row in the database.
type UploadModel struct {
	Id         int
	Hash       string
	Body       string
	FileNames  []string
	FileHashes []string
	Timestamp  int64
}

func init() {
	// Open the .env file and load the variables into the environment.
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file:", err)
	}

	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbUser := os.Getenv("DB_USER")
	dbPass := os.Getenv("DB_PASS")

	connStr := fmt.Sprintf("postgresql://%s:%s@%s?sslmode=require&port=%s", dbUser, dbPass, dbHost, dbPort)

	// Connect to the PostgreSQL database using the connection string.
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}

	// Create the database schema if it does not already exist.
	if err = initDB(db); err != nil {
		log.Fatal(err)
	}
}

func initDB(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS Uploads(
		id BIGSERIAL PRIMARY KEY,
		hash CHAR(40) NOT NULL UNIQUE,
		body TEXT,
		files TEXT ARRAY,
		timestamp BIGINT NOT NULL
	)
	`

	_, err := db.Exec(query)
	return err
}

func isValidHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// GetUpload fetches a row from the database matching the hash, by checking if the row's hash string begins with the hash parameter string.
// The hash must be a valid hex string in lowercase, and must have a length >= 10 and <= 40.
func GetUpload(hash string) (*UploadModel, error) {
	// Validate the hash before querying
	if len(hash) < 10 || len(hash) > 40 || !isValidHex(hash) {
		return nil, ErrHashInvalid
	}

	upload := new(UploadModel)
	var files []string

	// Fetch the row matching the hash parameter as a prefix or a perfect match.
	// Notice that it was not possible to write LIKE '$1%', as that would cause an error with our PostgreSQL driver, pq.
	// Instead, it was recommended to join the strings using the '||' operator.
	row := db.QueryRow("SELECT * FROM Uploads WHERE hash LIKE $1 || '%'", hash)
	if err := row.Scan(&upload.Id, &upload.Hash, &upload.Body, (*pq.StringArray)(&files), &upload.Timestamp); err != nil {
		return nil, err
	}

	// Separate the filenames from the hashes so we can pass it into the templates without issues.
	upload.FileNames = make([]string, len(files))
	upload.FileHashes = make([]string, len(files))
	for i, file := range files {
		parts := strings.Split(file, "/") // Example: mytextdocument.txt/9a3b4fa77a9c243f132ab23
		upload.FileNames[i] = parts[0]
		upload.FileHashes[i] = parts[1]
	}

	return upload, nil
}

// SubmitUpload creates a row in the database containing the plaintext body parameter and a sequence of filename/hash pairs.
// The plaintext body and filename/hash pairs are hashed together using SHA-1 to create uniqueness in the database.
func SubmitUpload(body string, fileNameHashPairs []string) (string, error) {
	// Combine the body and fileHashes into a single buffer.
	buffer := new(bytes.Buffer)
	buffer.WriteString(body)
	buffer.WriteString(strings.Join(fileNameHashPairs, ""))

	// Generate a hash of the buffer, which makes it unique to those exact files uploaded and/or the plaintext body.
	hash := fmt.Sprintf("%x", sha1.Sum(buffer.Bytes()))

	_, err := db.Exec("INSERT INTO Uploads(hash, body, files, timestamp) VALUES ($1, $2, $3, $4)", hash, body, (*pq.StringArray)(&fileNameHashPairs), time.Now().UTC().Unix())
	if err != nil {
		// See: https://www.postgresql.org/docs/current/protocol-error-fields.html
		if err, ok := err.(*pq.Error); ok {
			// See: https://www.postgresql.org/docs/current/errcodes-appendix.html
			switch err.Code {
			case "23505": // unique_violation
				// return "", ErrConstraintUnique
				return hash, nil // This thing already exists, so let's say we added it and redirect them to it.
			}
		}
		return "", err
	}

	return hash, nil
}
