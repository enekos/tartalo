if exists('b:did_indent')
  finish
endif
let b:did_indent = 1

setlocal indentexpr=TartaloIndent(v:lnum)
setlocal indentkeys=0{,0},0),0],!^F,o,O,e

let b:undo_indent = 'setlocal indentexpr< indentkeys<'

function! TartaloIndent(lnum) abort
  let prev = prevnonblank(a:lnum - 1)
  if prev == 0
    return 0
  endif

  let prevline = getline(prev)
  let currline = getline(a:lnum)

  let indent = indent(prev)

  " Increase indent after opening brace
  if prevline =~ '{[[:space:]]*$'
    let indent += shiftwidth()
  endif

  " Decrease indent for closing brace
  if currline =~ '^\s*}'
    let indent -= shiftwidth()
  endif

  return max([0, indent])
endfunction
