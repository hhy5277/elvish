// Derived from stdlib package text/template/parse.

// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// das source lexer and parser.
package parse

import (
	"os"
	"fmt"
	"strings"
	"strconv"
	"../util"
)

type Parser struct {
	Name      string    // name of the script represented by the tree.
	Root      Node // top-level root of the tree.
	Ctx       Context
	text      string    // text parsed to create the script (or its parent)
	tab       bool
	// Parsing only; cleared after parse.
	lex       *Lexer
	token     [3]Item // three-token lookahead for parser.
	peekCount int
}

// next returns the next token.
func (p *Parser) next() Item {
	if p.peekCount > 0 {
		p.peekCount--
	} else {
		p.token[0] = p.lex.NextItem()
	}
	return p.token[p.peekCount]
}

// backup backs the input stream up one token.
func (p *Parser) backup() {
	p.peekCount++
}

// backup2 backs the input stream up two tokens.
// The zeroth token is already there.
func (p *Parser) backup2(t1 Item) {
	p.token[1] = t1
	p.peekCount = 2
}

// backup3 backs the input stream up three tokens
// The zeroth token is already there.
func (p *Parser) backup3(t2, t1 Item) { // Reverse order: we're pushing back.
	p.token[1] = t1
	p.token[2] = t2
	p.peekCount = 3
}

// peek returns but does not consume the next token.
func (p *Parser) peek() Item {
	if p.peekCount > 0 {
		return p.token[p.peekCount-1]
	}
	p.peekCount = 1
	p.token[0] = p.lex.NextItem()
	return p.token[0]
}

// nextNonSpace returns the next non-space token.
func (p *Parser) nextNonSpace() (token Item) {
	for {
		token = p.next()
		if token.Typ != ItemSpace {
			break
		}
	}
	return token
}

// peekNonSpace returns but does not consume the next non-space token.
func (p *Parser) peekNonSpace() (token Item) {
	for {
		token = p.next()
		if token.Typ != ItemSpace {
			break
		}
	}
	p.backup()
	return token
}

// Parsing.

// NewParser allocates a new parse tree with the given name.
func NewParser(name string) *Parser {
	return &Parser{
		Name:  name,
	}
}

// errorf formats the error and terminates processing.
func (p *Parser) errorf(pos int, format string, args ...interface{}) {
	p.Root = nil
	panic(util.NewContextualError(p.Name, p.text, pos, format, args...))
}

// expect consumes the next token and guarantees it has the required type.
func (p *Parser) expect(expected ItemType, context string) Item {
	token := p.nextNonSpace()
	if token.Typ != expected {
		p.unexpected(token, context)
	}
	return token
}

// expectOneOf consumes the next token and guarantees it has one of the required types.
func (p *Parser) expectOneOf(expected1, expected2 ItemType, context string) Item {
	token := p.nextNonSpace()
	if token.Typ != expected1 && token.Typ != expected2 {
		p.unexpected(token, context)
	}
	return token
}

// unexpected complains about the token and terminates processing.
func (p *Parser) unexpected(token Item, context string) {
	p.errorf(int(token.Pos), "unexpected %s in %s", token, context)
}

// recover is the handler that turns panics into returns from the top level of Parse.
func (p *Parser) recover(errp **util.ContextualError) {
	e := recover()
	if e == nil {
		return
	}
	if _, ok := e.(*util.ContextualError); !ok {
		panic(e)
	}
	if p != nil {
		p.stopParse()
	}
	*errp = e.(*util.ContextualError)
}

// stopParse terminates parsing.
func (p *Parser) stopParse() {
	p.lex = nil
}

// Parse parses the script to construct a representation of the script for
// execution.
func (p *Parser) Parse(text string, tab bool) (tree *Parser, err *util.ContextualError) {
	defer p.recover(&err)

	p.text = text
	p.tab = tab
	p.lex = Lex(p.Name, text)
	p.peekCount = 0

	// TODO This now only parses a pipeline.
	p.Root = p.pipeline()

	p.stopParse()
	return p, nil
}

// Pipeline = [ Command { "|" Command } ]
func (p *Parser) pipeline() *ListNode {
	pipe := newList(p.peek().Pos)
	if p.peekNonSpace().Typ == ItemEOF {
		return pipe
	}
loop:
	for {
		n := p.command()
		pipe.append(n)

		switch token := p.next(); token.Typ {
		case ItemPipe:
			continue loop
		case ItemEndOfLine, ItemEOF:
			break loop
		default:
			p.unexpected(token, "end of pipeline")
		}
	}
	return pipe
}

// command parses a command.
// Command = TermList { [ space ] Redir }
func (p *Parser) command() *CommandNode {
	cmd := newCommand(p.peek().Pos)
	cmd.ListNode = *p.termList()
loop:
	for {
		switch p.peekNonSpace().Typ {
		case ItemRedirLeader:
			cmd.Redirs = append(cmd.Redirs, p.redir())
		default:
			break loop
		}
	}
	return cmd
}

// TermList = [ space ] Term { [ space ] Term } [ space ]
func (p *Parser) termList() *ListNode {
	list := newList(p.peek().Pos)
	list.append(p.term())
loop:
	for {
		if startsFactor(p.peekNonSpace().Typ) {
			list.append(p.term())
		} else {
			break loop
		}
	}
	return list
}

// Term = Factor { Factor | [ space ] '^' Factor [ space ] } [ space ]
func (p *Parser) term() *ListNode {
	term := newList(p.peek().Pos)
	term.append(p.factor())
loop:
	for {
		if startsFactor(p.peek().Typ) {
			term.append(p.factor())
		} else if p.peekNonSpace().Typ == ItemCaret {
			p.next()
			p.peekNonSpace()
			term.append(p.factor())
		} else {
			break loop
		}
	}
	return term
}

func unquote(token Item) (string, error) {
	switch token.Typ {
	case ItemBare:
		return token.Val, nil
	case ItemSingleQuoted:
		return strings.Replace(token.Val[1:len(token.Val)-1], "``", "`", -1),
		       nil
	case ItemDoubleQuoted:
		return strconv.Unquote(token.Val)
	default:
		return "", fmt.Errorf("Bad token type (%s)", token.Typ)
	}
}

// startsFactor determines whether a token of type p can start a Factor.
// Frequently used for lookahead, since a Term or TermList always starts with
// a Factor.
func startsFactor(p ItemType) bool {
	switch p {
	case ItemBare, ItemSingleQuoted, ItemDoubleQuoted,
			ItemLParen, ItemLBracket,
			ItemDollar:
		return true
	default:
		return false
	}
}

// Factor = '$' Factor
//        = ( bare | single-quoted | double-quoted | Table )
//        = ( '(' TermList ')' )
func (p *Parser) factor() (fn *FactorNode) {
	fn = newFactor(p.peek().Pos)
	for p.peek().Typ == ItemDollar {
		p.next()
		fn.Dollar++
	}
	switch token := p.next(); token.Typ {
	case ItemBare, ItemSingleQuoted, ItemDoubleQuoted:
		text, err := unquote(token)
		if err != nil {
			p.errorf(int(token.Pos), "%s", err)
		}
		if token.End & MayContinue != 0 {
			p.Ctx = NewArgContext(token.Val)
		} else {
			p.Ctx = nil
		}
		fn.Node = newString(token.Pos, token.Val, text)
		return
	case ItemLParen:
		fn.Node = p.termList()
		if token := p.next(); token.Typ != ItemRParen {
			p.unexpected(token, "factor of item list")
		}
		return
	case ItemLBracket:
		fn.Node = p.table()
		return
	default:
		p.unexpected(token, "factor")
		return nil
	}
}

// table parses a table literal. The opening bracket has been seen.
// Table = '[' { [ space ] ( Term [ space ] '=' [ space ] Term | Term ) [ space ] } ']'
// NOTE The '=' is actually special-cased Term.
func (p *Parser) table() (tn *TableNode) {
	tn = newTable(p.peek().Pos)

	for {
		token := p.nextNonSpace()
		if startsFactor(token.Typ) {
			p.backup()
			term := p.term()

			next := p.peekNonSpace()
			if next.Typ == ItemBare && next.Val == "=" {
				p.next()
				// New element of dict part. Skip spaces and find value term.
				p.peekNonSpace()
				valueTerm := p.term()
				tn.appendToDict(term, valueTerm)
			} else {
				// New element of list part.
				tn.appendToList(term)
			}
		} else if token.Typ == ItemRBracket {
			return
		} else {
			p.unexpected(token, "table literal")
		}
	}
}

// redir parses an IO redirection.
// Redir = redir-leader [ [ space ] Term ]
// NOTE The actual grammar is more complex than above, since 1) the inner
// structure of redir-leader is also parsed here, and 2) the Term is not truly
// optional, but sometimes required depending on the redir-leader.
func (p *Parser) redir() Redir {
	leader := p.next()

	// Partition the redirection leader into direction and qualifier parts.
	// For example, if leader.Val == ">>[1=2]", dir == ">>" and qual == "1=2".
	var dir, qual string

	if i := strings.IndexRune(leader.Val, '['); i != -1 {
		dir = leader.Val[:i]
		qual = leader.Val[i+1:len(leader.Val)-1]
	} else {
		dir = leader.Val
	}

	// Determine the flag and default (new) fd from the direction.
	var (
		fd uintptr
		flag int
	)

	switch dir {
	case "<":
		flag = os.O_RDONLY
		fd = 0
	case "<>":
		flag = os.O_RDWR | os.O_CREATE
		fd = 0
	case ">":
		flag = os.O_WRONLY | os.O_CREATE
		fd = 1
	case ">>":
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		fd = 1
	default:
		p.errorf(int(leader.Pos), "Unexpected redirection direction %q", dir)
	}

	if len(qual) > 0 {
		// Qualified redirection
		if i := strings.IndexRune(qual, '='); i != -1 {
			// FdRedir or CloseRedir
			lhs := qual[:i]
			rhs := qual[i+1:]
			if len(lhs) > 0 {
				var err error
				fd, err = Atou(lhs)
				if err != nil {
					// TODO identify precious position
					p.errorf(int(leader.Pos), "Invalid new fd in qualified redirection %q", lhs)
				}
			}
			if len(rhs) > 0 {
				oldfd, err := Atou(rhs)
				if err != nil {
					// TODO identify precious position
					p.errorf(int(leader.Pos), "Invalid old fd in qualified redirection %q", rhs)
				}
				return NewFdRedir(fd, oldfd)
			} else {
				return newCloseRedir(fd)
			}
		} else {
			// FilenameRedir with fd altered
			var err error
			fd, err = Atou(qual)
			if err != nil {
				// TODO identify precious position
				p.errorf(int(leader.Pos), "Invalid new fd in qualified redirection %q", qual)
			}
		}
	}
	// FilenameRedir
	p.peekNonSpace()
	return newFilenameRedir(fd, flag, p.term())
}
