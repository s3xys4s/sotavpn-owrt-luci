package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type storedRuleFile struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// Existing rows reference the previously saved content. Only a new upload
// carries text, so opening or saving the editor never retransmits old files.
type ruleFileInput struct {
	Name string  `json:"name"`
	Hash string  `json:"hash,omitempty"`
	Text *string `json:"text,omitempty"`
}

func (f storedRuleFile) reference() ruleFileInput {
	return ruleFileInput{Name: f.Name, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(f.Text)))}
}

func resolveRuleFiles(inputs []ruleFileInput, saved []storedRuleFile) ([]storedRuleFile, error) {
	if inputs == nil {
		return saved, nil
	}
	if len(inputs) > 32 {
		return nil, errors.New("Можно добавить до 32 файлов")
	}
	existing := make(map[ruleFileInput]storedRuleFile, len(saved))
	for _, f := range saved {
		existing[f.reference()] = f
	}
	var files []storedRuleFile
	size := 0
	for _, input := range inputs {
		file, ok := existing[input]
		if input.Text != nil {
			if input.Name == "" || len(input.Name) > 255 || strings.ContainsAny(input.Name, "/\\") || strings.IndexFunc(input.Name, unicode.IsControl) >= 0 {
				return nil, errors.New("Некорректное имя файла")
			}
			file = storedRuleFile{Name: input.Name, Text: *input.Text}
			if len(file.Text) > maxSourceBytes || !utf8.ValidString(file.Text) {
				return nil, errors.New("Нужен файл в UTF-8 размером до 2 МиБ")
			}
			if _, err := parseEntries(file.Text); err != nil {
				return nil, fmt.Errorf("%s: %w", file.Name, err)
			}
		} else if !ok {
			return nil, errors.New("Файл изменился. Загрузите настройки заново")
		}
		size += len(file.Text)
		if size > maxSourceTotal {
			return nil, errors.New("Общий размер файлов превышает 8 МиБ")
		}
		files = append(files, file)
	}
	return files, nil
}
