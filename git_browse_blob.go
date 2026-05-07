package gitserver

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/h2non/filetype"
)

type GitBrowserBlob struct {
	Blob     []byte
	BlobHash string
	FileName string
	FileType string
	MIMEType string
}

func (gb *GitBrowser) browseBlob() error {
	if gb.PageArgs == "" {
		return caddyhttp.Error(404, fmt.Errorf("no file specified in blob request"))
	}
	pageData := new(GitBrowserBlob)

	fileHash := plumbing.NewHash(gb.PageArgs)
	fileBlob, err := gb.Repo.BlobObject(fileHash)
	var fileName string
	if err != nil {
		// Try to see if file path
		refCommit, _ := gb.Repo.CommitObject(*gb.RefHash)
		tree, err := refCommit.Tree()
		if err != nil {
			return caddyhttp.Error(503, err)
		}
		file, err := tree.File(gb.PageArgs)
		if err != nil {
			return caddyhttp.Error(503, err)
		}
		fileHash = file.Hash
		fileBlob = &file.Blob
		fileName = file.Name
	} else {
		// Extract filename from blob
		refCommit, _ := gb.Repo.CommitObject(*gb.RefHash)
		files, err := refCommit.Files()
		if err != nil {
			return caddyhttp.Error(503, err)
		}
		for file, err := files.Next(); file != nil; {
			if err != nil {
				return caddyhttp.Error(503, err)
			}
			if file.Hash.String() == fileHash.String() {
				fileName = file.Name
				break
			}
		}
	}
	blobReader, err := fileBlob.Reader()
	bufBlobReader := bufio.NewReader(blobReader)
	if err != nil {
		return caddyhttp.Error(503, err)
	}
	// Read file header into buffer to determine type
	fileHead := make([]byte, 261)
	bufBlobReader.Read(fileHead)

	blobType, err := filetype.Match(fileHead)
	if err != nil {
		return caddyhttp.Error(503, err)
	}

	if blobType.MIME.Type == "image" {
		// Embed raw uri with image html tag
		pageData.FileType = "image"
		pageData.MIMEType = blobType.MIME.Value
		pageData.Blob = []byte("/" + gb.Root + "/raw/" + fileHash.String())
	} else if blobType.MIME.Type == "application" && blobType.MIME.Subtype != "pdf" {
		println("application:", fileName, blobType.MIME.Value)
		pageData.FileType = "application"
		pageData.MIMEType = blobType.MIME.Value
		pageData.Blob = []byte("/" + gb.Root + "/raw/" + fileHash.String())

	} else if path.Ext(fileName) == ".md" {
		pageData.FileType = "markdown"

		inputMarkdown := make([]byte, fileBlob.Size)
		blobReader, _ = fileBlob.Reader()
		_, err := blobReader.Read(inputMarkdown)
		if err != nil {
			return caddyhttp.Error(503, err)
		}

		// if bytes.Contains(inputMarkdown, []byte("![")) {
		// 	inputMarkdown = bytes.ReplaceAll(inputMarkdown, []byte("]("), []byte("](/"+gb.Root+"/raw/"))
		// }

		// marked := blackfriday.Run(inputMarkdown)

		pageData.Blob = inputMarkdown
	} else if path.Ext(fileName) == ".pdf" {
		pageData.FileType = "pdf"
		pageData.Blob = []byte("/" + gb.Root + "/raw/" + fileHash.String())
	} else if path.Ext(fileName) == ".kicad_sch" || path.Ext(fileName) == ".kicad_pcb" {
		pageData.FileType = "kicad"
		pageData.Blob = []byte("/" + gb.Root + "/raw/" + gb.PageArgs)
	} else if path.Ext(fileName) == ".kicad_pro" {
		pageData.FileType = "kicad_pro"
		projectName, found := strings.CutSuffix(gb.PageArgs, "pro")
		if !found {
			return caddyhttp.Error(503, errors.New("Unknown kicad project file"))
		}
		pageData.Blob = []byte("/" + gb.Root + "/raw/" + projectName)
	} else {
		pageData.FileType = "text"
		strBuilder := new(strings.Builder)
		// strBuilder.Write(fileHead)
		blobReader, _ = fileBlob.Reader()
		bufBlobReader.Reset(blobReader)
		_, err = io.Copy(strBuilder, bufBlobReader)
		if err != nil {
			return caddyhttp.Error(503, err)
		}
		pageData.Blob = []byte(strBuilder.String())
	}

	pageData.BlobHash = fileHash.String()
	pageData.FileName = fileName

	gb.PageData = pageData

	return nil
}

func (gb *GitBrowser) browseRaw() error {
	if gb.PageArgs == "" {
		return caddyhttp.Error(404, fmt.Errorf("no file specified in blob request"))
	}
	pageData := new(GitBrowserBlob)
	fileHash := plumbing.NewHash(gb.PageArgs)
	fileBlob, err := gb.Repo.BlobObject(fileHash)
	// if err != nil {
	// 	return caddyhttp.Error(404, err)
	// }
	// var fileName string
	if err != nil {
		// Try to see if file path
		refCommit, _ := gb.Repo.CommitObject(*gb.RefHash)
		tree, err := refCommit.Tree()
		if err != nil {
			return caddyhttp.Error(503, err)
		}
		file, err := tree.File(gb.PageArgs)
		if err != nil {
			return caddyhttp.Error(503, err)
		}
		fileHash = file.Hash
		fileBlob = &file.Blob
		// fileName = file.Name
	} //else {
	// Extract filename from blob
	// 	refCommit, _ := gb.Repo.CommitObject(*gb.RefHash)
	// 	files, err := refCommit.Files()
	// 	if err != nil {
	// 		return caddyhttp.Error(503, err)
	// 	}
	// 	for file, err := files.Next(); file != nil; {
	// 		if err != nil {
	// 			return caddyhttp.Error(503, err)
	// 		}
	// 		if file.Hash.String() == fileHash.String() {
	// 			fileName = file.Name
	// 			break
	// 		}
	// 	}
	// }

	blobReader, err := fileBlob.Reader()
	bufBlobReader := bufio.NewReader(blobReader)
	if err != nil {
		return caddyhttp.Error(503, err)
	}
	// Read file header into buffer to determine type
	fileHead := make([]byte, 261)
	bufBlobReader.Read(fileHead)

	fileType, err := filetype.Match(fileHead)
	if err != nil {
		return caddyhttp.Error(503, err)
	}
	// Save MIMEtype
	pageData.FileType = fileType.MIME.Type

	// Create array as long as file
	pageData.Blob = make([]byte, fileBlob.Size)

	// Read file
	blobReader, _ = fileBlob.Reader()
	bufBlobReader.Reset(blobReader)

	bufBlobReader.Read(pageData.Blob)

	gb.PageData = pageData

	return nil
}

func (gb *GitBrowser) serveRaw(w http.ResponseWriter) error {
	pageData := gb.PageData.(*GitBrowserBlob)
	w.Header().Set("Content-Type", pageData.MIMEType)
	w.Write(pageData.Blob)
	return nil
}
