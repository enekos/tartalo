if exists('b:did_ftplugin')
  finish
endif
let b:did_ftplugin = 1

setlocal expandtab tabstop=2 shiftwidth=2 softtabstop=2
setlocal commentstring=//\ %s
setlocal formatoptions-=t formatoptions+=croql
setlocal foldmethod=indent

let b:undo_ftplugin = 'setlocal et< ts< sw< sts< cms< fo< fdm<'
