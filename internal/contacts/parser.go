package contacts

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/xuri/excelize/v2"

	"visnyk/internal/cascade"
	"visnyk/internal/normalizer"
)

var splitRe = regexp.MustCompile(`[\n,;\t]+`)

// ParseText splits raw text by newline, comma, semicolon, tab and normalizes each token.
// Only phone numbers are processed — names are ignored.
func ParseText(raw string) ParseResult {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ParseResult{}
	}
	lines := strings.Split(raw, "\n")
	seen := make(map[string]struct{})
	var contacts []cascade.Contact
	var invalid []InvalidEntry
	dup := 0
	rowIdx := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rowIdx++
		parts := splitRe.Split(line, -1)
		var tokens []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				tokens = append(tokens, p)
			}
		}
		if len(tokens) == 0 {
			continue
		}
		// Expand tokens containing spaces. If token without letters and normalizes as phone, keep as one.
		var expanded []string
		for _, t := range tokens {
			if strings.Contains(t, " ") {
				hasLetter := false
				for _, r := range t {
					if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= 'А' && r <= 'я') || r == 'ґ' || r == 'Ґ' || r == 'є' || r == 'Є' || r == 'і' || r == 'І' || r == 'ї' || r == 'Ї' {
						hasLetter = true
						break
					}
				}
				if !hasLetter {
					if _, err := normalizer.Normalize(t); err == nil {
						expanded = append(expanded, t)
						continue
					}
				}
				sub := strings.Fields(t)
				expanded = append(expanded, sub...)
			} else {
				expanded = append(expanded, t)
			}
		}
		// Only phones — names ignored
		foundPhone := false
		for _, tok := range expanded {
			norm, err := normalizer.Normalize(tok)
			if err == nil {
				foundPhone = true
				if _, exists := seen[norm]; exists {
					dup++
					continue
				}
				seen[norm] = struct{}{}
				contacts = append(contacts, cascade.Contact{Name: "", PhoneRaw: tok, NormalizedPhone: norm})
			}
		}
		if !foundPhone {
			invalid = append(invalid, InvalidEntry{Row: rowIdx, Raw: line, Error: "no valid phone found"})
		}
	}
	total := len(contacts) + len(invalid) + dup
	return ParseResult{Contacts: contacts, Invalid: invalid, Duplicates: dup, Total: total}
}

// ParseCSV parses CSV content with delimiter auto-detect (, ; \t).
func ParseCSV(r io.Reader) ParseResult {
	data, err := io.ReadAll(r)
	if err != nil {
		return ParseResult{Invalid: []InvalidEntry{{Row: 1, Raw: "", Error: err.Error()}}}
	}
	s := string(data)
	// Strip BOM
	s = strings.TrimPrefix(s, "\xEF\xBB\xBF")
	s = strings.TrimSpace(s)
	if s == "" {
		return ParseResult{}
	}
	delim := detectDelimiter(s)
	reader := csv.NewReader(strings.NewReader(s))
	reader.Comma = delim
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil && len(rows) == 0 {
		// fallback to text parsing
		return ParseText(s)
	}
	// Detect header
	startRow := 0
	if len(rows) > 0 && isHeaderRow(rows[0]) {
		startRow = 1
	}
	seen := make(map[string]struct{})
	var contacts []cascade.Contact
	var invalid []InvalidEntry
	dup := 0
	total := 0

	// Determine phone column by scoring
	phoneCol := detectPhoneColumn(rows, startRow)

	for i := startRow; i < len(rows); i++ {
		row := rows[i]
		if len(row) == 0 {
			continue
		}
		empty := true
		for _, c := range row {
			if strings.TrimSpace(c) != "" {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		total++
		// Only phones — scan all cells for phones, ignore names
		// Prefer phoneCol if it normalizes, otherwise scan all cells
		var phoneRaw string
		var norm string
		var found bool
		if phoneCol >= 0 && phoneCol < len(row) {
			cand := strings.TrimSpace(row[phoneCol])
			if cand != "" {
				if n, err := normalizer.Normalize(cand); err == nil {
					phoneRaw = cand
					norm = n
					found = true
				}
			}
		}
		if !found {
			// scan all cells for first valid phone; also handle cells containing multiple numbers separated by delimiters/spaces
			for _, c := range row {
				c = strings.TrimSpace(c)
				if c == "" {
					continue
				}
				if n, err := normalizer.Normalize(c); err == nil {
					phoneRaw = c
					norm = n
					found = true
					break
				}
				// try split cell by delimiters/spaces
				for _, tok := range splitRe.Split(c, -1) {
					tok = strings.TrimSpace(tok)
					if tok == "" {
						continue
					}
					if n, err := normalizer.Normalize(tok); err == nil {
						phoneRaw = tok
						norm = n
						found = true
						break
					}
					for _, sub := range strings.Fields(tok) {
						if n, err := normalizer.Normalize(sub); err == nil {
							phoneRaw = sub
							norm = n
							found = true
							break
						}
					}
					if found {
						break
					}
				}
				if found {
					break
				}
			}
		}
		if !found {
			invalid = append(invalid, InvalidEntry{Row: i + 1, Raw: strings.Join(row, string(delim)), Error: "no valid phone found"})
			continue
		}
		if _, exists := seen[norm]; exists {
			dup++
			continue
		}
		seen[norm] = struct{}{}
		contacts = append(contacts, cascade.Contact{Name: "", PhoneRaw: phoneRaw, NormalizedPhone: norm})
	}
	return ParseResult{Contacts: contacts, Invalid: invalid, Duplicates: dup, Total: total}
}

func detectDelimiter(s string) rune {
	// Look at first few lines
	lines := strings.SplitN(s, "\n", 5)
	countComma, countSemi, countTab := 0, 0, 0
	for _, l := range lines {
		countComma += strings.Count(l, ",")
		countSemi += strings.Count(l, ";")
		countTab += strings.Count(l, "\t")
	}
	if countSemi > countComma && countSemi > countTab {
		return ';'
	}
	if countTab > countComma && countTab > countSemi {
		return '\t'
	}
	return ','
}

func isHeaderRow(row []string) bool {
	phoneHints := []string{"телефон", "phone", "номер", "tel", "моб"}
	nameHints := []string{"имя", "ім'я", "name", "фио", "піб", "контакт"}
	hasHint := false
	hasDigit := false
	for _, c := range row {
		lc := strings.ToLower(strings.TrimSpace(c))
		for _, h := range phoneHints {
			if strings.Contains(lc, h) {
				hasHint = true
			}
		}
		for _, h := range nameHints {
			if strings.Contains(lc, h) {
				hasHint = true
			}
		}
		if strings.ContainsAny(c, "0123456789+") {
			// if cell contains digit and normalizes, likely not header
			if _, err := normalizer.Normalize(c); err == nil {
				hasDigit = true
			}
		}
	}
	return hasHint && !hasDigit
}

func detectPhoneColumn(rows [][]string, startRow int) int {
	if len(rows) <= startRow {
		return 0
	}
	colCount := 0
	for _, r := range rows {
		if len(r) > colCount {
			colCount = len(r)
		}
	}
	if colCount <= 1 {
		return 0
	}
	bestCol := -1
	bestScore := -1
	for col := 0; col < colCount; col++ {
		score := 0
		for i := startRow; i < len(rows) && i < startRow+10; i++ {
			if col >= len(rows[i]) {
				continue
			}
			c := strings.TrimSpace(rows[i][col])
			if c == "" {
				continue
			}
			if _, err := normalizer.Normalize(c); err == nil {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestCol = col
		}
	}
	if bestScore <= 0 {
		return 0
	}
	return bestCol
}

// ParseXLSX parses XLSX file content (bytes).
func ParseXLSX(data []byte) ParseResult {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return ParseResult{Invalid: []InvalidEntry{{Row: 1, Raw: "", Error: fmt.Sprintf("open xlsx: %v", err)}}}
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return ParseResult{Invalid: []InvalidEntry{{Row: 1, Raw: "", Error: "no sheets"}}}
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return ParseResult{Invalid: []InvalidEntry{{Row: 1, Raw: "", Error: err.Error()}}}
	}
	if len(rows) == 0 {
		return ParseResult{}
	}
	startRow := 0
	if isHeaderRow(rows[0]) {
		startRow = 1
	}
	phoneCol := detectPhoneColumn(rows, startRow)
	seen := make(map[string]struct{})
	var contacts []cascade.Contact
	var invalid []InvalidEntry
	dup := 0
	total := 0
	for i := startRow; i < len(rows); i++ {
		row := rows[i]
		if len(row) == 0 {
			continue
		}
		empty := true
		for _, c := range row {
			if strings.TrimSpace(c) != "" {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		total++
		var phoneRaw string
		var norm string
		var found bool
		if phoneCol >= 0 && phoneCol < len(row) {
			cand := strings.TrimSpace(row[phoneCol])
			if cand != "" {
				if n, err := normalizer.Normalize(cand); err == nil {
					phoneRaw = cand
					norm = n
					found = true
				}
			}
		}
		if !found {
			for _, c := range row {
				c = strings.TrimSpace(c)
				if c == "" {
					continue
				}
				if n, err := normalizer.Normalize(c); err == nil {
					phoneRaw = c
					norm = n
					found = true
					break
				}
				for _, tok := range splitRe.Split(c, -1) {
					tok = strings.TrimSpace(tok)
					if tok == "" {
						continue
					}
					if n, err := normalizer.Normalize(tok); err == nil {
						phoneRaw = tok
						norm = n
						found = true
						break
					}
					for _, sub := range strings.Fields(tok) {
						if n, err := normalizer.Normalize(sub); err == nil {
							phoneRaw = sub
							norm = n
							found = true
							break
						}
					}
					if found {
						break
					}
				}
				if found {
					break
				}
			}
		}
		if !found {
			invalid = append(invalid, InvalidEntry{Row: i + 1, Raw: strings.Join(row, ","), Error: "no valid phone found"})
			continue
		}
		if _, exists := seen[norm]; exists {
			dup++
			continue
		}
		seen[norm] = struct{}{}
		contacts = append(contacts, cascade.Contact{Name: "", PhoneRaw: phoneRaw, NormalizedPhone: norm})
	}
	return ParseResult{Contacts: contacts, Invalid: invalid, Duplicates: dup, Total: total}
}

// ParseFile auto-detects by filename extension.
func ParseFile(filename string, data []byte) ParseResult {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".xlsx") || strings.HasSuffix(lower, ".xls"):
		return ParseXLSX(data)
	case strings.HasSuffix(lower, ".csv"):
		return ParseCSV(bytes.NewReader(data))
	default:
		// txt or unknown -> try CSV first, fallback to text
		res := ParseCSV(bytes.NewReader(data))
		if len(res.Contacts) > 0 || len(res.Invalid) > 0 {
			return res
		}
		return ParseText(string(data))
	}
}
