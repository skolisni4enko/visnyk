package contacts

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestParseText_SingleColumn(t *testing.T) {
	res := ParseText("+380991234567\n+380681112233\n380991234568")
	if len(res.Contacts) != 3 {
		t.Fatalf("want 3, got %d invalid=%v", len(res.Contacts), res.Invalid)
	}
	if res.Contacts[0].NormalizedPhone != "+380991234567" {
		t.Errorf("first phone mismatch %s", res.Contacts[0].NormalizedPhone)
	}
}

func TestParseText_WithNames(t *testing.T) {
	// Now only phones are processed — names ignored, but phones still extracted
	res := ParseText("Ivan +380991234567\nOlena, 0991234568")
	if len(res.Contacts) != 2 {
		t.Fatalf("want 2 got %d %+v", len(res.Contacts), res)
	}
	if res.Contacts[0].Name != "" {
		t.Errorf("names should be ignored, want empty got %q", res.Contacts[0].Name)
	}
	if res.Contacts[0].NormalizedPhone != "+380991234567" {
		t.Errorf("phone mismatch %q", res.Contacts[0].NormalizedPhone)
	}
	if res.Contacts[1].NormalizedPhone != "+380991234568" {
		t.Errorf("phone mismatch %q", res.Contacts[1].NormalizedPhone)
	}
}

func TestParseText_Dedup(t *testing.T) {
	res := ParseText("+380991234567\n099 123 45 67\n+380991234567")
	if len(res.Contacts) != 1 {
		t.Fatalf("want 1 after dedup got %d", len(res.Contacts))
	}
	if res.Duplicates != 2 {
		t.Errorf("want 2 dups got %d", res.Duplicates)
	}
}

func TestParseText_Delimiters(t *testing.T) {
	res := ParseText("+380991234567, +380991234568;+380991234569\t+380991234570")
	if len(res.Contacts) != 4 {
		t.Fatalf("want 4 got %d", len(res.Contacts))
	}
}

func TestParseText_Invalid(t *testing.T) {
	res := ParseText("notaphone\n123\n+380991234567")
	if len(res.Contacts) != 1 {
		t.Fatalf("want 1 valid got %d", len(res.Contacts))
	}
	if len(res.Invalid) != 2 {
		t.Fatalf("want 2 invalid got %d", len(res.Invalid))
	}
}

func TestParseCSV_Comma(t *testing.T) {
	csvData := "Name,Phone\nIvan,+380991234567\nOlena,0991234568"
	res := ParseCSV(strings.NewReader(csvData))
	if len(res.Contacts) != 2 {
		t.Fatalf("want 2 got %d invalid=%v", len(res.Contacts), res.Invalid)
	}
	if res.Contacts[0].Name != "" {
		t.Errorf("names should be ignored, want empty got %q", res.Contacts[0].Name)
	}
	if res.Contacts[0].NormalizedPhone != "+380991234567" {
		t.Errorf("phone mismatch %q", res.Contacts[0].NormalizedPhone)
	}
}

func TestParseCSV_Semicolon(t *testing.T) {
	csvData := "Ім'я;Телефон\nІван;+380991234567\nПетро;0991234568"
	res := ParseCSV(strings.NewReader(csvData))
	if len(res.Contacts) != 2 {
		t.Fatalf("want 2 got %d %+v", len(res.Contacts), res.Invalid)
	}
}

func TestParseCSV_SingleColumn(t *testing.T) {
	csvData := "+380991234567\n+380991234568\ninvalid"
	res := ParseCSV(strings.NewReader(csvData))
	if len(res.Contacts) != 2 {
		t.Fatalf("want 2 got %d", len(res.Contacts))
	}
	if len(res.Invalid) != 1 {
		t.Fatalf("want 1 invalid got %d", len(res.Invalid))
	}
}

func TestParseCSV_Dedup(t *testing.T) {
	csvData := "Phone\n+380991234567\n0991234567"
	res := ParseCSV(strings.NewReader(csvData))
	if len(res.Contacts) != 1 {
		t.Fatalf("want 1 after dedup got %d", len(res.Contacts))
	}
	if res.Duplicates != 1 {
		t.Errorf("want 1 dup got %d", res.Duplicates)
	}
}

func TestParseXLSX(t *testing.T) {
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	f.SetCellValue(sheet, "A1", "Name")
	f.SetCellValue(sheet, "B1", "Phone")
	f.SetCellValue(sheet, "A2", "Ivan")
	f.SetCellValue(sheet, "B2", "+380991234567")
	f.SetCellValue(sheet, "A3", "Olena")
	f.SetCellValue(sheet, "B3", "0991234568")
	f.SetCellValue(sheet, "A4", "Bad")
	f.SetCellValue(sheet, "B4", "notaphone")
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	res := ParseXLSX(buf.Bytes())
	if len(res.Contacts) != 2 {
		t.Fatalf("want 2 got %d invalid=%v", len(res.Contacts), res.Invalid)
	}
	if len(res.Invalid) != 1 {
		t.Fatalf("want 1 invalid got %d", len(res.Invalid))
	}
}

func TestParseXLSX_PhoneFirstColumn(t *testing.T) {
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	f.SetCellValue(sheet, "A1", "+380991234567")
	f.SetCellValue(sheet, "A2", "+380991234568")
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	res := ParseXLSX(buf.Bytes())
	if len(res.Contacts) != 2 {
		t.Fatalf("want 2 got %d", len(res.Contacts))
	}
}

func TestParseFile_Auto(t *testing.T) {
	csvData := []byte("Phone\n+380991234567")
	res := ParseFile("test.csv", csvData)
	if len(res.Contacts) != 1 {
		t.Fatalf("csv auto want 1 got %d", len(res.Contacts))
	}
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	f.SetCellValue(sheet, "A1", "+380991234567")
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	res = ParseFile("test.xlsx", buf.Bytes())
	if len(res.Contacts) != 1 {
		t.Fatalf("xlsx auto want 1 got %d", len(res.Contacts))
	}
	// txt fallback
	res = ParseFile("test.txt", []byte("+380991234567\n+380991234568"))
	if len(res.Contacts) != 2 {
		t.Fatalf("txt want 2 got %d", len(res.Contacts))
	}
}
