//go:build imaptest

package imap2

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
)

var (
	imaptestBinary = flag.String("test.imaptest", "", "Path to imaptest binary")
	imaptestDir    = flag.String("test.imaptest-dir", "", "Path to imaptest scripted tests directory")
	imaptestRawLog = flag.Bool("test.imaptest-rawlog", false, "Write rawlog")
)

type scriptedTest struct {
	Name string

	TestFile string
	MboxFile string
}

func TestImaptestScripted(t *testing.T) {
	if *imaptestBinary == "" {
		t.Skip("No imaptest binary specified")
	}
	if *imaptestDir == "" {
		t.Skip("No imaptest scripted tests dir specified")
	}

	var err error
	*imaptestDir, err = filepath.Abs(*imaptestDir)
	if err != nil {
		t.Fatal(err)
	}

	scriptedFiles, err := os.ReadDir(*imaptestDir)
	if err != nil {
		t.Fatal(err)
	}
	tests := make(map[string]*scriptedTest, len(scriptedFiles))
	// Collect test files.
	for _, file := range scriptedFiles {
		if file.IsDir() {
			continue
		}
		ext := filepath.Ext(file.Name())
		if ext == "" {
			tests[file.Name()] = &scriptedTest{
				Name:     file.Name(),
				TestFile: filepath.Join(*imaptestDir, file.Name()),
				MboxFile: filepath.Join(*imaptestDir, "default.mbox"),
			}
		}
	}
	// Override mbox files if relevant
	for _, file := range scriptedFiles {
		if file.IsDir() {
			continue
		}
		ext := filepath.Ext(file.Name())
		if ext == ".mbox" {
			test, ok := tests[file.Name()]
			if !ok {
				continue
			}
			test.MboxFile = filepath.Join(*imaptestDir, file.Name())
		}
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			t.Log("Test temporary directory:", dir)

			if err := os.Mkdir(filepath.Join(dir, "test"), 0777); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(test.TestFile, filepath.Join(dir, "test", test.Name)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(test.MboxFile, filepath.Join(dir, "test", test.Name+".mbox")); err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}

			srv := utils.TestServer(t)
			port := srv.RunTCP()
			defer srv.Close()
			username, password := srv.Account()

			args := []string{
				"host=127.0.0.1",
				"port=" + strconv.Itoa(port),
				"user=" + username,
				"pass=" + password,
				"test=" + filepath.Join(dir, "test"),
			}
			if *imaptestRawLog {
				args = append(args, "rawlog")
			}

			var imaptestOut bytes.Buffer
			cmd := exec.Command(*imaptestBinary, args...)
			cmd.Stdout = &imaptestOut
			cmd.Stderr = utils.TestLogWriter(t, "imaptest")
			t.Log("running", cmd.Path, cmd.Args)
			if err := cmd.Run(); err != nil {
				t.Error(err)
			}

			t.Log(imaptestOut.String())

			if strings.Contains(imaptestOut.String(), "base protocol: 0/0 individual commands failed") &&
				strings.Contains(imaptestOut.String(), "extensions: 0/0 individual commands failed") {

				t.Skip("Empty test, probably missing capacility")
			}

			if *imaptestRawLog {
				dirContents, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				for _, f := range dirContents {
					if f.IsDir() {
						continue
					}
					if strings.HasPrefix(f.Name(), "rawlog") {
						t.Log(f.Name(), "contents:")
						rawLogBlob, err := os.ReadFile(filepath.Join(dir, f.Name()))
						if err != nil {
							t.Fatal(err)
						}
						t.Log(string(rawLogBlob))
					}
				}
			}
		})
	}
}
