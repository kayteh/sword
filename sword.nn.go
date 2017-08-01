package main

import (
	"os"
	"fmt"
)
import ("bufio";"io";"strings")
type frame struct {
  i int
  s string
  line, column int
}
type Lexer struct {
  // The lexer runs in its own goroutine, and communicates via channel 'ch'.
  ch chan frame
  ch_stop chan bool
  // We record the level of nesting because the action could return, and a
  // subsequent call expects to pick up where it left off. In other words,
  // we're simulating a coroutine.
  // TODO: Support a channel-based variant that compatible with Go's yacc.
  stack []frame
  stale bool

  // The 'l' and 'c' fields were added for
  // https://github.com/wagerlabs/docker/blob/65694e801a7b80930961d70c69cba9f2465459be/buildfile.nex
  // Since then, I introduced the built-in Line() and Column() functions.
  l, c int

  parseResult interface{}

  // The following line makes it easy for scripts to insert fields in the
  // generated code.
  // [NEX_END_OF_LEXER_STRUCT]
}

// NewLexerWithInit creates a new Lexer object, runs the given callback on it,
// then returns it.
func NewLexerWithInit(in io.Reader, initFun func(*Lexer)) *Lexer {
  yylex := new(Lexer)
  if initFun != nil {
    initFun(yylex)
  }
  yylex.ch = make(chan frame)
  yylex.ch_stop = make(chan bool, 1)
  var scan func(in *bufio.Reader, ch chan frame, ch_stop chan bool, family []dfa, line, column int) 
  scan = func(in *bufio.Reader, ch chan frame, ch_stop chan bool, family []dfa, line, column int) {
    // Index of DFA and length of highest-precedence match so far.
    matchi, matchn := 0, -1
    var buf []rune
    n := 0
    checkAccept := func(i int, st int) bool {
      // Higher precedence match? DFAs are run in parallel, so matchn is at most len(buf), hence we may omit the length equality check.
      if family[i].acc[st] && (matchn < n || matchi > i) {
        matchi, matchn = i, n
        return true
      }
      return false
    }
    var state [][2]int
    for i := 0; i < len(family); i++ {
      mark := make([]bool, len(family[i].startf))
      // Every DFA starts at state 0.
      st := 0
      for {
        state = append(state, [2]int{i, st})
        mark[st] = true
        // As we're at the start of input, follow all ^ transitions and append to our list of start states.
        st = family[i].startf[st]
        if -1 == st || mark[st] { break }
        // We only check for a match after at least one transition.
        checkAccept(i, st)
      }
    }
    atEOF := false
    stopped := false
    for {
      if n == len(buf) && !atEOF {
        r,_,err := in.ReadRune()
        switch err {
        case io.EOF: atEOF = true
        case nil:    buf = append(buf, r)
        default:     panic(err)
        }
      }
      if !atEOF {
        r := buf[n]
        n++
        var nextState [][2]int
        for _, x := range state {
          x[1] = family[x[0]].f[x[1]](r)
          if -1 == x[1] { continue }
          nextState = append(nextState, x)
          checkAccept(x[0], x[1])
        }
        state = nextState
      } else {
dollar:  // Handle $.
        for _, x := range state {
          mark := make([]bool, len(family[x[0]].endf))
          for {
            mark[x[1]] = true
            x[1] = family[x[0]].endf[x[1]]
            if -1 == x[1] || mark[x[1]] { break }
            if checkAccept(x[0], x[1]) {
              // Unlike before, we can break off the search. Now that we're at the end, there's no need to maintain the state of each DFA.
              break dollar
            }
          }
        }
        state = nil
      }

      if state == nil {
        lcUpdate := func(r rune) {
          if r == '\n' {
            line++
            column = 0
          } else {
            column++
          }
        }
        // All DFAs stuck. Return last match if it exists, otherwise advance by one rune and restart all DFAs.
        if matchn == -1 {
          if len(buf) == 0 {  // This can only happen at the end of input.
            break
          }
          lcUpdate(buf[0])
          buf = buf[1:]
        } else {
          text := string(buf[:matchn])
          buf = buf[matc