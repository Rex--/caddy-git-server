package gitserver

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/object"
	"go.uber.org/zap"
)

// Serve a git client
func (gs *GitServer) serveGitClient(repoPath string, w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	// Log the clone attempt
	gs.logger.Info("serving git client",
		zap.String("path", r.RequestURI),
		zap.String("method", r.Method),
		zap.String("git_repo", repoPath),
		zap.String("git_protocol", r.Header.Get("Git-Protocol")),
		zap.String("git_client", r.UserAgent()),
	)

	// Only serve v0 because we dont support v2 yet
	return gs.serveGitV0(repoPath, w, r, next)

	// Serve v0 or v2 based on header
	// if gs.Protocol == "smart" || (r.Header.Get("Git-Protocol") == "version=2" && gs.Protocol == "both") {
	// 	return gs.serveGitV2(repoPath, w, r, next)
	// } else if gs.Protocol == "dumb" || gs.Protocol == "both" {
	// 	// Serve dumb v0 protocol
	// } else {
	// 	return nil
	// }
}

// Serve dumb git client files. These are generated on-the-fly
func (gs *GitServer) serveGitV0(repoPath string, w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	// repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)

	// root := repl.ReplaceAll(gs.Root, ".")

	// Detect 'info/refs' and generate and serve
	if strings.HasSuffix(r.URL.Path, "info/refs") {
		// Try to open repo
		repo, err := git.PlainOpen(repoPath)
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, fmt.Errorf("could not load repository"))
		}

		// // Log the clone attempt
		// gs.logger.Info("git clone attempt",
		// 	zap.String("path", r.RequestURI),
		// 	zap.String("git_repo", repoPath),
		// 	zap.String("git_protocol", r.Header.Get("Git-Protocol")),
		// 	zap.String("git_client", r.UserAgent()),
		// )

		var refs []string

		// Collect all heads in repo
		repoHeads, err := repo.Branches()
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}
		// Write heads to connection
		repoHeads.ForEach(func(r *plumbing.Reference) error {
			fmt.Fprintf(w, "%s\t%s\n", r.Hash().String(), r.Name().String())
			refs = append(refs, r.String())
			return nil
		})

		// Collect all tags in repo
		repoTags, err := repo.Tags()
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}
		// Write tags to connection
		repoTags.ForEach(func(r *plumbing.Reference) error {
			fmt.Fprintf(w, "%s\t%s\n", r.Hash().String(), r.Name().String())
			refs = append(refs, r.String())
			return nil
		})

		gs.logger.Debug("generating dumb info/refs",
			zap.String("git_repo", repoPath),
			zap.String("req_path", r.URL.Path),
			zap.String("refs", strings.Join(refs, ",")),
		)

		// The approach below is without a git library //
		// // Find all ref files in GIT_DIR/refs/*/*
		// refPath := filepath.Join(gitDir, "refs/*/*")
		// refFiles, err := filepath.Glob(refPath)
		// if err != nil {
		// 	return err
		// }

		// // Generate 'info/refs'
		// var infoRefs string
		// for _, s := range refFiles {
		// 	refDirs, refName := filepath.Split(s)
		// 	_, refDir := filepath.Split(strings.TrimSuffix(refDirs, "/"))
		// 	refName = filepath.Join("refs", refDir, refName)
		// 	refHash, err := os.ReadFile(s)
		// 	if err != nil {
		// 		return err
		// 	}
		// 	infoRefs += strings.TrimSpace(string(refHash)) + "\t" + refName + "\n"
		// }

		// // Write info/refs to connection and close it
		// fmt.Fprintf(w, "%s", infoRefs)
		//                                             //
		return nil
	}

	// Detect 'objects/info/packs' and generate and serve
	if strings.HasSuffix(r.URL.Path, "objects/info/packs") {

		// Try to open repo
		_, err := git.PlainOpen(repoPath)
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}

		// Get packs in repo
		packFiles, err := filepath.Glob(filepath.Join(repoPath, "objects/pack/*.pack"))
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}

		// Write pack file response
		for _, packFile := range packFiles {
			fmt.Fprintf(w, "P %s\n", filepath.Base(packFile))
		}

		return nil
	}

	// Serve the file if it exists
	return gs.FileServer.ServeHTTP(w, r, next)
}

// Serve git protocol v2
func (gs *GitServer) serveGitV2(repoPath string, w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	if strings.HasSuffix(r.URL.Path, "/info/refs") {
		// Initial Client request to "%GIT_URL/info/refs?service=git-upload-pack"
		// Return advertised capabilities
		return gs.serveGitV2Capabilities(w, r)
	}

	// Client is requesting command
	if strings.HasSuffix(r.URL.Path, "/git-upload-pack") {

		// Read command
		pktLenBytes := make([]byte, 4)
		r.Body.Read(pktLenBytes)
		commandLen64, err := strconv.ParseUint(string(pktLenBytes), 16, 32)
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}
		commandBytes := make([]byte, commandLen64-4)
		r.Body.Read(commandBytes)
		commandBytes = bytes.TrimSpace(commandBytes)
		println(string(commandBytes))

		// Read capability list until the delimeter packet is reached ("0001")
		var pktLen uint64
		r.Body.Read(pktLenBytes)
		pktLen, err = strconv.ParseUint(string(pktLenBytes), 16, 32)
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}
		for pktLen != 1 {
			capabilityBytes := make([]byte, pktLen-4)
			r.Body.Read(capabilityBytes)
			fmt.Printf("capability: %s\n", capabilityBytes)
			r.Body.Read(pktLenBytes)
			pktLen, err = strconv.ParseUint(string(pktLenBytes), 16, 32)
			if err != nil {
				return caddyhttp.Error(http.StatusInternalServerError, err)
			}
		}

		// Hand off to command handler
		switch string(commandBytes[8:]) {
		case "ls-refs":
			return gs.serveGitV2LsRefs(repoPath, w, r)

		case "fetch":
			return gs.serveGitV2Fetch(repoPath, w, r)

		}

		println("Remaining body:")
		bodyBytes := make([]byte, 1024)
		r.Body.Read(bodyBytes)
		println(string(bodyBytes))
	}

	return nil
}

// Serve v2 protocol capabilities
func (gs *GitServer) serveGitV2Capabilities(w http.ResponseWriter, r *http.Request) error {

	// Set content type and cache control headers
	w.Header().Add("Content-Type", "application/x-git-upload-pack-advertisement")
	w.Header().Add("Cache-Control", "no-cache")

	e := pktline.NewEncoder(w)

	// Write v2 version string
	e.EncodeString("version 2")
	// fmt.Fprint(w, "000eversion 2\n")

	// Write capability list
	e.EncodeString(
		"agent=caddy-git-server/0.0.0",
		"ls-refs",
		"fetch",
	)
	// fmt.Fprint(w, "0021agent=caddy-git-server/0.0.0\n")
	// fmt.Fprint(w, "000cls-refs\n")
	// fmt.Fprint(w, "000afetch\n")

	// Write flush packet
	return e.Flush()
	// fmt.Fprint(w, "0000")

	// return nil
}

// Serve v2 protocol ls-refs command
func (gs *GitServer) serveGitV2LsRefs(repoPath string, w http.ResponseWriter, r *http.Request) error {

	// Try to open repo
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, fmt.Errorf("could not load repository"))
	}

	var refs []string

	// Collect all heads in repo
	repoHeads, err := repo.Branches()
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	// Write heads to connection
	repoHeads.ForEach(func(r *plumbing.Reference) error {
		tknLen := len(r.Hash().String()) + len(r.Name().String()) + 6 // 4 extra for length, 1 for space, one for newline
		fmt.Fprintf(w, "%.4x%s %s\n", tknLen, r.Hash().String(), r.Name().String())
		refs = append(refs, r.String())
		return nil
	})

	// Collect all tags in repo
	repoTags, err := repo.Tags()
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	// Write tags to connection
	repoTags.ForEach(func(r *plumbing.Reference) error {
		tknLen := len(r.Hash().String()) + len(r.Name().String()) + 6 // 4 extra for length, 1 for space, one for newline
		fmt.Fprintf(w, "%.4x%s %s\n", tknLen, r.Hash().String(), r.Name().String())
		refs = append(refs, r.String())
		return nil
	})
	// println(strings.Join(refs, ", "))

	// Write flush packet
	fmt.Fprint(w, "0000")

	return nil
}

func (gs *GitServer) serveGitV2Fetch(repoPath string, w http.ResponseWriter, r *http.Request) error {

	e := pktline.NewEncoder(w)

	// Print arguments and get list of wanted object hashed
	var pktLen uint64
	var wanted []string
	var objHashes []plumbing.Hash
	var done = false
	pktLenBytes := make([]byte, 4)
	r.Body.Read(pktLenBytes)
	pktLen, err := strconv.ParseUint(string(pktLenBytes), 16, 32)
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	for pktLen != 0 {

		// Read packet line
		argBytes := make([]byte, pktLen-4)
		r.Body.Read(argBytes)
		argBytes = bytes.TrimSpace(argBytes)
		switch string(argBytes[:4]) {
		case "want":
			wanted = append(wanted, string(argBytes[5:]))
			objHashes = append(objHashes, plumbing.NewHash(string(argBytes[5:])))
			fmt.Printf("want: %s", argBytes[5:])
			if plumbing.IsHash(string(argBytes[5:])) {
				fmt.Println(" valid")
			} else {
				fmt.Println(" invalid!")
			}
		case "done":
			done = true
			fmt.Print("done\n")
		default:
			fmt.Printf("argument: %s\n", argBytes)
		}

		// Read next packet line length
		r.Body.Read(pktLenBytes)
		pktLen, err = strconv.ParseUint(string(pktLenBytes), 16, 32)
		if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}
	}

	// Return ACKs if not done
	if !done {
		e.EncodeString("acknowledgements")
		for _, h := range wanted {
			e.Encodef("ACK %s", h)
		}
		e.EncodeString("ready")
		w.Write([]byte{'0', '0', '0', '1'})
	}

	// Return wanted-refs

	fmt.Printf("wanted: %v\n", wanted)

	// Open repository
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		fmt.Printf("err: %v\n", err)
		return caddyhttp.Error(http.StatusInternalServerError, fmt.Errorf("could not load repository"))
	}

	// Search commit for objects
	c, err := repo.CommitObject(objHashes[0])
	t, err := c.Tree()
	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}
	fmt.Printf("tree: %s\n", t.Hash.String())
	objHashes = append(objHashes, t.Hash)

	tw := object.NewTreeWalker(t, false, make(map[plumbing.Hash]bool))

	for {
		name, entry, err := tw.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return caddyhttp.Error(http.StatusInternalServerError, err)
		}
		fmt.Printf("object: %s %s\n", name, entry.Hash.String())
		objHashes = append(objHashes, entry.Hash)
	}

	// Return packfile

	e.EncodeString("packfile")

	// Create packfile encoder
	// pe := packfile.NewEncoder(w, repo.Storer, false)

	f, err := os.Create("test.pack")
	if err != nil {
		fmt.Printf("err: %v\n", err)
		caddyhttp.Error(http.StatusInternalServerError, err)
	}
	fe := packfile.NewEncoder(f, repo.Storer, false)

	// Write packfile with objects to writer
	// _, err = pe.Encode(objHashes, 0)
	// if err != nil {
	// 	fmt.Printf("err: %v\n", err)
	// 	caddyhttp.Error(http.StatusInternalServerError, err)
	// }
	_, err = fe.Encode(objHashes, 0)
	if err != nil {
		fmt.Printf("err: %v\n", err)
		caddyhttp.Error(http.StatusInternalServerError, err)
	}
	f.Close()

	packfile, err := os.ReadFile("test.pack")
	if err != nil {
		fmt.Printf("err: %v\n", err)
		caddyhttp.Error(http.StatusInternalServerError, err)
	}

	fmt.Fprintf(w, "%.4x", len(packfile)+5)
	w.Write([]byte{1})
	w.Write(packfile)

	e.Flush()

	return nil
}
