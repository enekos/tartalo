if exists('b:current_syntax')
  finish
endif

" --- Keywords ---
syn keyword tartaloKeyword   let const func return if else for in match
syn keyword tartaloKeyword   import export defer as
syn keyword tartaloKeyword   tool agent parallel task
syn keyword tartaloKeyword   test

syn keyword tartaloType      string number float bool void

syn keyword tartaloBoolean   true false
syn keyword tartaloNull      null

" --- Comments ---
syn match   tartaloComment   "//.*$" contains=tartaloTodo
syn keyword tartaloTodo      TODO FIXME HACK NOTE XXX contained

" --- Strings ---
" Highlight the interpolation placeholder ${...} inside strings
syn region  tartaloInterp    matchgroup=tartaloInterpDelim
  \ start="\${" end="}"
  \ contained contains=TOP

syn region  tartaloString    start='"' skip='\\"' end='"'
  \ contains=tartaloEscape,tartaloInterp,@Spell

syn match   tartaloEscape    /\\[ntr\\"$]/ contained

" --- Backtick command literals ---
syn region  tartaloCommand   start='`' end='`'
  \ contains=tartaloEscape

" --- Numbers ---
syn match   tartaloNumber    "\<[0-9]\+\>"

" --- Operators ---
syn match   tartaloOp        /??/
syn match   tartaloOp        /|>/
syn match   tartaloOp        /\.\./
syn match   tartaloOp        /==\|!=\|<=\|>=/
syn match   tartaloOp        /&&\|||/
syn match   tartaloOp        /=>/
syn match   tartaloOp        /[+\-*\/%]/
syn match   tartaloOp        /[<>]/
syn match   tartaloOp        /!\ze[^=a-z]/ " logical not (not effect annotation)

" --- Effect annotations (!ai, !net, !fs:read, etc.) ---
syn match   tartaloEffect    "!\(ai\|net\|io\|exec\|fs:[a-z:]*\)"

" --- Generics: <T> type parameters ---
syn match   tartaloGeneric   "<[A-Z][A-Za-z0-9_]*>"

" --- Built-in functions ---
syn keyword tartaloBuiltin
  \ echo eprint exit
  \ str num floatOf intOf
  \ len upper lower trim replace contains startsWith endsWith
  \ slice byteLen byteSlice split join
  \ parseFloat formatFloat floor ceil round
  \ vSum vMean vMin vMax vVar vStd
  \ vAdd vSub vMul vScale vDot linspace arange cumsum
  \ map filter reduce count unique
  \ readFile writeFile appendFile removeFile mkdir listDir
  \ exists isFile isDir stat readStdin
  \ pathJoin basename dirname extname parsePath
  \ exec execTimeout fetch
  \ regexMatch regexFind regexFindAll regexReplace
  \ readCsv writeCsv
  \ env args now sleep formatTime
  \ jsonGet jsonHas jsonArray jsonEscape
  \ llm approval trace spawnAgent callTool agentTools toolSchemas
  \ assertEq assertNe check fail skip
  \ mockExec mockExecCalls mockFetch mockFetchCalls
  \ mockReadFile mockReadFileCalls mockEnv mockNow mockArgs
  \ mockReadStdin mockLlm mockLlmCalls
  \ desc budget

" --- Type declarations (PascalCase identifiers after 'type' keyword) ---
syn match   tartaloTypeName  "\<[A-Z][A-Za-z0-9_]*\>"

" --- Highlighting links ---
hi def link tartaloKeyword     Keyword
hi def link tartaloType        Type
hi def link tartaloBoolean     Boolean
hi def link tartaloNull        Constant
hi def link tartaloComment     Comment
hi def link tartaloTodo        Todo
hi def link tartaloString      String
hi def link tartaloEscape      SpecialChar
hi def link tartaloInterp      Normal
hi def link tartaloInterpDelim Delimiter
hi def link tartaloCommand     PreProc
hi def link tartaloNumber      Number
hi def link tartaloOp          Operator
hi def link tartaloEffect      Special
hi def link tartaloGeneric     Special
hi def link tartaloBuiltin     Function
hi def link tartaloTypeName    Type

let b:current_syntax = 'tartalo'
