package imap2

import (
	"testing"

	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
)

func TestFolderCreate(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	c := s.Conn()
	defer c.Close()
	c.ExpectPattern(`\* *`)
	c.Login(username, password)

	c.Writeln(". CREATE testfolder")
	c.ExpectOK()

	c.Writeln(". CREATE testfolder/subfolder")
	c.ExpectOK()

	c.Writeln(". CREATE testfolder2/subfolder")
	c.ExpectOK()

	c.Writeln(". CREATE testfolder2")
	c.ExpectNO("ALREADYEXISTS")

	c.Writeln(`. LIST "%" ""`)
	c.ExpectPattern(`\* LIST (\\HasChildren) "/" "testfolder"`)
	c.ExpectPattern(`\* LIST (\\HasNoChildren) "/" "testfolder/subfolder"`)
	c.ExpectPattern(`\* LIST (\\HasChildren) "/" "testfolder2"`)
	c.ExpectPattern(`\* LIST (\\HasNoChildren) "/" "testfolder2/subfolder"`)
	c.ExpectOK()
}

func TestFolderDelete(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	c := s.Conn()
	defer c.Close()
	c.ExpectPattern(`\* *`)
	c.Login(username, password)

	c.Writeln(". CREATE testfolder")
	c.ExpectOK()
	c.Writeln(". CREATE testfolder2/subfolder")
	c.ExpectOK()

	c.Writeln(". DELETE testfolder2")
	c.ExpectNO("HASCHILDREN")

	c.Writeln(". DELETE testfolder")
	c.ExpectOK()

	c.Writeln(`. LIST "%" ""`)
	c.ExpectPattern(`\* LIST (\\HasChildren) "/" "testfolder2"`)
	c.ExpectPattern(`\* LIST (\\HasNoChildren) "/" "testfolder2/subfolder"`)
	c.ExpectOK()
}

func TestFolderRename(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	c := s.Conn()
	defer c.Close()
	c.ExpectPattern(`\* *`)
	c.Login(username, password)

	c.Writeln(". CREATE testfolder")
	c.ExpectOK()
	c.Writeln(". CREATE testfolder2/subfolder")
	c.ExpectOK()

	c.Writeln(". RENAME testfolder2/subfolder testfolder2/renamed")
	c.ExpectOK()

	c.Writeln(". RENAME testfolder2/renamed testfolder/subfolder")
	c.ExpectOK()

	c.Writeln(". RENAME testfolder2/renamed testfolder/subfolder")
	c.ExpectNO("NONEXISTENT")

	c.Writeln(". RENAME testfolder/subfolder testfolder/subfolder/subfolder")
	c.ExpectNO("CANNOT")

	c.Writeln(". RENAME testfolder/subfolder testfolder2/intermediate/renamed")
	c.ExpectOK()

	c.Writeln(". RENAME testfolder2 testfolder3")
	c.ExpectOK()

	c.Writeln(`. LIST "%" ""`)
	c.ExpectPattern(`\* LIST (\\HasNoChildren) "/" "testfolder"`)
	c.ExpectPattern(`\* LIST (\\HasChildren) "/" "testfolder3"`)
	c.ExpectPattern(`\* LIST (\\HasChildren) "/" "testfolder3/intermediate"`)
	c.ExpectPattern(`\* LIST (\\HasNoChildren) "/" "testfolder3/intermediate/renamed"`)
	c.ExpectOK()
}
