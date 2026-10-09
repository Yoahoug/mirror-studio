package web

import (
	"strings"
)

type tokenKind uint8

const (
	tokText tokenKind = iota
	tokVar
	tokTag
	tokComment
)

type token struct {
	kind  tokenKind
	value string
}

type lexer struct {
	source string
	pos    int
}

func lexTemplate(source string) ([]token, error) {
	lexer := &lexer{source: source}
	tokens := []token{}
	for {
		next := strings.IndexAny(lexer.source[lexer.pos:], "{")
		if next < 0 {
			if lexer.pos < len(lexer.source) {
				tokens = append(tokens, token{kind: tokText, value: lexer.source[lexer.pos:]})
			}
			return tokens, nil
		}
		next += lexer.pos
		if next > lexer.pos {
			tokens = append(tokens, token{kind: tokText, value: lexer.source[lexer.pos:next]})
		}
		if next+1 >= len(lexer.source) {
			tokens = append(tokens, token{kind: tokText, value: lexer.source[next:]})
			return tokens, nil
		}
		opener := lexer.source[next : next+2]
		var closer string
		switch opener {
		case "{{":
			closer = "}}"
		case "{%":
			closer = "%}"
		case "{#":
			closer = "#}"
		default:
			tokens = append(tokens, token{kind: tokText, value: lexer.source[next : next+1]})
			lexer.pos = next + 1
			continue
		}
		end := strings.Index(lexer.source[next+2:], closer)
		if end < 0 {
			// The reference lexer only recognises a complete delimiter pair, so
			// an unterminated opener stays literal text.
			tokens = append(tokens, token{kind: tokText, value: lexer.source[next : next+2]})
			lexer.pos = next + 2
			continue
		}
		end += next + 2
		inner := strings.TrimSpace(lexer.source[next+2 : end])
		kind := tokVar
		switch opener {
		case "{%":
			kind = tokTag
		case "{#":
			kind = tokComment
		}
		if kind != tokComment {
			tokens = append(tokens, token{kind: kind, value: inner})
		}
		lexer.pos = end + 2
	}
}
