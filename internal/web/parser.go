package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type filterCall struct {
	name   string
	arg    string
	hasArg bool
}

type filterExpression struct {
	literal    any
	isLiteral  bool
	path       []string
	pathText   string
	filters    []filterCall
	autoEscape bool
}

func parseFilterExpression(source string) *filterExpression {
	expression := &filterExpression{autoEscape: true}
	parts := splitUnquoted(source, '|')
	head := strings.TrimSpace(parts[0])
	if len(parts) == 1 && isLiteralToken(head) {
		expression.isLiteral = true
		expression.literal = literalValue(head)
	} else {
		expression.pathText = head
		if head != "" {
			expression.path = strings.Split(head, ".")
		}
	}
	for _, raw := range parts[1:] {
		call := strings.TrimSpace(raw)
		name, argument, hasArg := cutFilterArg(call)
		expression.filters = append(expression.filters, filterCall{name: name, arg: argument, hasArg: hasArg})
	}
	return expression
}

func cutFilterArg(call string) (string, string, bool) {
	index := strings.IndexByte(call, ':')
	if index < 0 {
		return call, "", false
	}
	name := strings.TrimSpace(call[:index])
	argument := strings.TrimSpace(call[index+1:])
	if len(argument) >= 2 && (argument[0] == '"' || argument[0] == '\'') && argument[len(argument)-1] == argument[0] {
		argument = argument[1 : len(argument)-1]
	}
	return name, argument, true
}

func splitUnquoted(source string, separator byte) []string {
	parts := []string{}
	current := strings.Builder{}
	quote := byte(0)
	for index := 0; index < len(source); index++ {
		char := source[index]
		switch {
		case quote != 0:
			if char == quote {
				quote = 0
			}
			current.WriteByte(char)
		case char == '"' || char == '\'':
			quote = char
			current.WriteByte(char)
		case char == separator:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteByte(char)
		}
	}
	parts = append(parts, current.String())
	return parts
}

func isLiteralToken(token string) bool {
	if token == "" {
		return false
	}
	if token[0] == '"' || token[0] == '\'' {
		return len(token) >= 2 && token[len(token)-1] == token[0]
	}
	if token == "True" || token == "False" || token == "None" {
		return true
	}
	_, err := strconv.ParseFloat(token, 64)
	return err == nil
}

func literalValue(token string) any {
	switch token {
	case "True":
		return true
	case "False":
		return false
	case "None":
		return nil
	}
	if token[0] == '"' || token[0] == '\'' {
		return token[1 : len(token)-1]
	}
	if number, err := strconv.ParseInt(token, 10, 64); err == nil {
		return number
	}
	if number, err := strconv.ParseFloat(token, 64); err == nil {
		return number
	}
	return nil
}

func (e *filterExpression) resolve(state *renderState) (any, bool) {
	var value any
	found := true
	if e.isLiteral {
		value = e.literal
	} else {
		head := firstSegment(e.path)
		if head == "csrf_token" && state.onCSRF != nil {
			// django.template.context_processors.csrf calls get_token lazily, so
			// merely referencing csrf_token in a template is what makes Django
			// emit the cookie. The shells use {{ csrf_token }}, the forms use
			// {% csrf_token %}; both must mark it used.
			state.onCSRF()
		}
		value, found = state.scope.lookup(head)
		if found && len(e.path) > 1 {
			value, found = resolvePath(value, e.path[1:])
		}
		if !found {
			// The reference uses string_if_invalid (an empty string) for a
			// variable that does not resolve, then runs the filters on it.
			value = ""
		}
	}
	for _, call := range e.filters {
		value = applyFilter(call, value, state)
	}
	return value, found
}

func firstSegment(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[0]
}

func applyFilter(call filterCall, value any, state *renderState) any {
	switch call.name {
	case "safe":
		return safeHTML(stringify(value))
	case "default":
		if !truthy(value) {
			return call.arg
		}
		return value
	case "date":
		moment, ok := value.(time.Time)
		if !ok || moment.IsZero() {
			if ok {
				return ""
			}
			if text, isText := value.(string); isText && text != "" {
				if parsed, ok := parseTimeValue(text); ok {
					return formatDateTime(parsed, call.arg, state.location)
				}
			}
			return ""
		}
		return formatDateTime(moment, call.arg, state.location)
	}
	return value
}

func formatDateTime(moment time.Time, layout string, location *time.Location) string {
	if location != nil {
		moment = moment.In(location)
	}
	out := strings.Builder{}
	for index := 0; index < len(layout); index++ {
		char := layout[index]
		if char == '\\' && index+1 < len(layout) {
			index++
			out.WriteByte(layout[index])
			continue
		}
		switch char {
		case 'Y':
			out.WriteString(fmt.Sprintf("%04d", moment.Year()))
		case 'y':
			out.WriteString(fmt.Sprintf("%02d", moment.Year()%100))
		case 'm':
			out.WriteString(fmt.Sprintf("%02d", int(moment.Month())))
		case 'n':
			out.WriteString(strconv.Itoa(int(moment.Month())))
		case 'd':
			out.WriteString(fmt.Sprintf("%02d", moment.Day()))
		case 'j':
			out.WriteString(strconv.Itoa(moment.Day()))
		case 'H':
			out.WriteString(fmt.Sprintf("%02d", moment.Hour()))
		case 'G':
			out.WriteString(strconv.Itoa(moment.Hour()))
		case 'i':
			out.WriteString(fmt.Sprintf("%02d", moment.Minute()))
		case 's':
			out.WriteString(fmt.Sprintf("%02d", moment.Second()))
		default:
			out.WriteByte(char)
		}
	}
	return out.String()
}

func parseTimeValue(text string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// ── conditions ──

type term struct {
	expression *filterExpression
	literal    any
	isLiteral  bool
	nested     conditionNode
}

func (t term) value(state *renderState) any {
	if t.nested != nil {
		return t.nested.evaluate(state)
	}
	if t.isLiteral {
		return t.literal
	}
	resolved, _ := t.expression.resolve(state)
	return resolved
}

type conditionNode interface {
	evaluate(state *renderState) bool
}

type orCondition struct{ parts []conditionNode }

func (c orCondition) evaluate(state *renderState) bool {
	for _, part := range c.parts {
		if part.evaluate(state) {
			return true
		}
	}
	return false
}

type andCondition struct{ parts []conditionNode }

func (c andCondition) evaluate(state *renderState) bool {
	for _, part := range c.parts {
		if !part.evaluate(state) {
			return false
		}
	}
	return true
}

type notCondition struct{ inner conditionNode }

func (c notCondition) evaluate(state *renderState) bool { return !c.inner.evaluate(state) }

type truthCondition struct{ item term }

func (c truthCondition) evaluate(state *renderState) bool { return truthy(c.item.value(state)) }

type comparisonCondition struct {
	op          string
	left, right term
}

func (c comparisonCondition) evaluate(state *renderState) bool {
	return compareValues(c.op, c.left.value(state), c.right.value(state))
}

type conditionExpression struct{ root conditionNode }

func (e *conditionExpression) evaluate(state *renderState) bool { return e.root.evaluate(state) }

func parseCondition(source string) (*conditionExpression, error) {
	tokens := tokenizeCondition(source)
	position := 0
	root, err := parseOr(tokens, &position)
	if err != nil {
		return nil, err
	}
	if position != len(tokens) {
		return nil, fmt.Errorf("unexpected token %q in condition", tokens[position])
	}
	return &conditionExpression{root: root}, nil
}

func tokenizeCondition(source string) []string {
	tokens := []string{}
	current := strings.Builder{}
	quote := byte(0)
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for index := 0; index < len(source); index++ {
		char := source[index]
		switch {
		case quote != 0:
			current.WriteByte(char)
			if char == quote {
				quote = 0
			}
		case char == '"' || char == '\'':
			quote = char
			current.WriteByte(char)
		case char == ' ' || char == '\t' || char == '\n' || char == '\r':
			flush()
		case char == '=' || char == '!' || char == '<' || char == '>':
			if index+1 < len(source) && source[index+1] == '=' {
				flush()
				tokens = append(tokens, string([]byte{char, '='}))
				index++
				continue
			}
			if char == '!' {
				current.WriteByte(char)
				continue
			}
			flush()
			tokens = append(tokens, string(char))
		case char == '(' || char == ')':
			flush()
			tokens = append(tokens, string(char))
		default:
			current.WriteByte(char)
		}
	}
	flush()
	return tokens
}

func parseOr(tokens []string, position *int) (conditionNode, error) {
	parts := []conditionNode{}
	for {
		part, err := parseAnd(tokens, position)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
		if *position < len(tokens) && tokens[*position] == "or" {
			*position++
			continue
		}
		break
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return orCondition{parts: parts}, nil
}

func parseAnd(tokens []string, position *int) (conditionNode, error) {
	parts := []conditionNode{}
	for {
		part, err := parseNot(tokens, position)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
		if *position < len(tokens) && tokens[*position] == "and" {
			*position++
			continue
		}
		break
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return andCondition{parts: parts}, nil
}

func parseNot(tokens []string, position *int) (conditionNode, error) {
	if *position < len(tokens) && tokens[*position] == "not" && !(*position+1 < len(tokens) && tokens[*position+1] == "in") {
		*position++
		inner, err := parseNot(tokens, position)
		if err != nil {
			return nil, err
		}
		return notCondition{inner: inner}, nil
	}
	return parseComparison(tokens, position)
}

func parseComparison(tokens []string, position *int) (conditionNode, error) {
	left, err := parsePrimary(tokens, position)
	if err != nil {
		return nil, err
	}
	if *position >= len(tokens) {
		return truthCondition{item: left}, nil
	}
	operator := tokens[*position]
	if operator == "not" && *position+1 < len(tokens) && tokens[*position+1] == "in" {
		operator = "not in"
		*position += 2
	} else {
		switch operator {
		case "==", "!=", "<", ">", "<=", ">=", "in":
			*position++
		default:
			return truthCondition{item: left}, nil
		}
	}
	right, err := parsePrimary(tokens, position)
	if err != nil {
		return nil, err
	}
	return comparisonCondition{op: operator, left: left, right: right}, nil
}

func parsePrimary(tokens []string, position *int) (term, error) {
	if *position >= len(tokens) {
		return term{}, fmt.Errorf("unexpected end of condition")
	}
	token := tokens[*position]
	if token == "(" {
		*position++
		inner, err := parseOr(tokens, position)
		if err != nil {
			return term{}, err
		}
		if *position >= len(tokens) || tokens[*position] != ")" {
			return term{}, fmt.Errorf("missing closing parenthesis")
		}
		*position++
		return term{nested: inner}, nil
	}
	*position++
	if isLiteralToken(token) {
		return term{isLiteral: true, literal: literalValue(token)}, nil
	}
	return term{expression: parseFilterExpression(token)}, nil
}

func compareValues(operator string, left, right any) bool {
	switch operator {
	case "==":
		return equalValues(left, right)
	case "!=":
		return !equalValues(left, right)
	case "in":
		return containsValue(right, left)
	case "not in":
		return !containsValue(right, left)
	}
	leftNumber, leftOK := numericValue(left)
	rightNumber, rightOK := numericValue(right)
	if leftOK && rightOK {
		switch operator {
		case "<":
			return leftNumber < rightNumber
		case ">":
			return leftNumber > rightNumber
		case "<=":
			return leftNumber <= rightNumber
		case ">=":
			return leftNumber >= rightNumber
		}
		return false
	}
	leftText, rightText := stringify(left), stringify(right)
	switch operator {
	case "<":
		return leftText < rightText
	case ">":
		return leftText > rightText
	case "<=":
		return leftText <= rightText
	case ">=":
		return leftText >= rightText
	}
	return false
}

func equalValues(left, right any) bool {
	if leftNumber, ok := numericValue(left); ok {
		if rightNumber, ok := numericValue(right); ok {
			return leftNumber == rightNumber
		}
	}
	if leftText, ok := left.(string); ok {
		if rightText, ok := right.(string); ok {
			return leftText == rightText
		}
	}
	if leftBool, ok := left.(bool); ok {
		if rightBool, ok := right.(bool); ok {
			return leftBool == rightBool
		}
	}
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return stringify(left) == stringify(right)
}

func containsValue(container, needle any) bool {
	switch typed := container.(type) {
	case string:
		return strings.Contains(typed, stringify(needle))
	case []any:
		for _, item := range typed {
			if equalValues(item, needle) {
				return true
			}
		}
		return false
	case []map[string]any:
		for _, item := range typed {
			if equalValues(item, needle) {
				return true
			}
		}
		return false
	case map[string]any:
		_, ok := typed[stringify(needle)]
		return ok
	}
	return false
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	}
	return 0, false
}

// ── template parsing ──

type nodeParser struct {
	name   string
	tokens []token
	pos    int
	blocks map[string][]node
	parent string
}

func parseTemplate(name, source string) (*Template, error) {
	tokens, err := lexTemplate(source)
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", name, err)
	}
	parser := &nodeParser{name: name, tokens: tokens, blocks: map[string][]node{}}
	nodes, terminator, err := parser.parseUntil(nil)
	if err != nil {
		return nil, err
	}
	if terminator != "" {
		return nil, fmt.Errorf("template %s: unexpected tag %q", name, terminator)
	}
	return &Template{name: name, nodes: nodes, blocks: parser.blocks, parent: parser.parent}, nil
}

func (p *nodeParser) parseUntil(stops map[string]bool) ([]node, string, error) {
	nodes := []node{}
	for p.pos < len(p.tokens) {
		current := p.tokens[p.pos]
		switch current.kind {
		case tokText:
			nodes = append(nodes, textNode(current.value))
			p.pos++
		case tokComment:
			p.pos++
		case tokVar:
			nodes = append(nodes, varNode{expr: parseFilterExpression(current.value)})
			p.pos++
		case tokTag:
			keyword, rest := splitKeyword(current.value)
			if stops != nil && stops[keyword] {
				return nodes, current.value, nil
			}
			parsed, err := p.parseTag(keyword, rest)
			if err != nil {
				return nil, "", fmt.Errorf("template %s: %w", p.name, err)
			}
			if parsed != nil {
				nodes = append(nodes, parsed)
			}
		}
	}
	if stops != nil {
		return nil, "", fmt.Errorf("template %s: missing closing tag", p.name)
	}
	return nodes, "", nil
}

func (p *nodeParser) parseTag(keyword, rest string) (node, error) {
	switch keyword {
	case "extends":
		p.parent = unquote(strings.TrimSpace(rest))
		p.pos++
		return nil, nil
	case "block":
		name := unquote(strings.TrimSpace(rest))
		p.pos++
		body, terminator, err := p.parseUntil(map[string]bool{"endblock": true})
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(terminator, "endblock") {
			return nil, fmt.Errorf("block %s is not closed", name)
		}
		p.pos++
		if strings.TrimSpace(strings.TrimPrefix(terminator, "endblock")) == "" {
			p.blocks[name] = body
		} else if unquote(strings.TrimSpace(strings.TrimPrefix(terminator, "endblock"))) == name {
			p.blocks[name] = body
		} else {
			return nil, fmt.Errorf("block %s closed by %q", name, terminator)
		}
		return &blockNode{name: name, body: body}, nil
	case "if":
		return p.parseIf(rest)
	case "for":
		return p.parseFor(rest)
	case "include":
		name := firstQuoted(rest)
		if name == "" {
			return nil, fmt.Errorf("include requires a template name")
		}
		p.pos++
		return includeNode{name: name}, nil
	case "csrf_token":
		p.pos++
		return csrfNode{}, nil
	}
	return nil, fmt.Errorf("unsupported tag %q", keyword)
}

func (p *nodeParser) parseIf(condition string) (node, error) {
	p.pos++
	expression, err := parseCondition(condition)
	if err != nil {
		return nil, err
	}
	result := &ifNode{}
	body, terminator, err := p.parseUntil(map[string]bool{"elif": true, "else": true, "endif": true})
	if err != nil {
		return nil, err
	}
	result.branches = append(result.branches, ifBranch{condition: expression, body: body})
	for {
		keyword, rest := splitKeyword(terminator)
		switch keyword {
		case "elif":
			p.pos++
			next, err := parseCondition(rest)
			if err != nil {
				return nil, err
			}
			branchBody, nextTerminator, err := p.parseUntil(map[string]bool{"elif": true, "else": true, "endif": true})
			if err != nil {
				return nil, err
			}
			result.branches = append(result.branches, ifBranch{condition: next, body: branchBody})
			terminator = nextTerminator
		case "else":
			p.pos++
			fallback, nextTerminator, err := p.parseUntil(map[string]bool{"endif": true})
			if err != nil {
				return nil, err
			}
			result.fallback = fallback
			terminator = nextTerminator
		case "endif":
			p.pos++
			return result, nil
		default:
			return nil, fmt.Errorf("if is not closed")
		}
	}
}

func (p *nodeParser) parseFor(clause string) (node, error) {
	namesPart, sequencePart, ok := strings.Cut(clause, " in ")
	if !ok {
		return nil, fmt.Errorf("for tag requires 'in'")
	}
	names := []string{}
	for _, name := range strings.Split(namesPart, ",") {
		trimmed := strings.TrimSpace(name)
		if trimmed != "" {
			names = append(names, trimmed)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("for tag requires a loop variable")
	}
	p.pos++
	body, terminator, err := p.parseUntil(map[string]bool{"empty": true, "endfor": true})
	if err != nil {
		return nil, err
	}
	result := &forNode{names: names, seq: parseFilterExpression(strings.TrimSpace(sequencePart)), body: body}
	if keyword, _ := splitKeyword(terminator); keyword == "empty" {
		p.pos++
		empty, next, err := p.parseUntil(map[string]bool{"endfor": true})
		if err != nil {
			return nil, err
		}
		if keyword, _ := splitKeyword(next); keyword != "endfor" {
			return nil, fmt.Errorf("for is not closed")
		}
		result.empty = empty
	}
	p.pos++
	return result, nil
}

func splitKeyword(source string) (string, string) {
	trimmed := strings.TrimSpace(source)
	index := strings.IndexAny(trimmed, " \t\n\r")
	if index < 0 {
		return trimmed, ""
	}
	return trimmed[:index], strings.TrimSpace(trimmed[index+1:])
}

func unquote(value string) string {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}

func firstQuoted(source string) string {
	for index := 0; index < len(source); index++ {
		if source[index] != '"' && source[index] != '\'' {
			continue
		}
		end := strings.IndexByte(source[index+1:], source[index])
		if end < 0 {
			return ""
		}
		return source[index+1 : index+1+end]
	}
	return ""
}
