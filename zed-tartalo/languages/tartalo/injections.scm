; Tartalo language injections
; Highlights shell code inside command literals

; Inject bash into command literal content
((command_literal
  (cmd_content) @injection.content)
 (#set! injection.language "bash"))

; Also inject bash into the entire command literal for better highlighting
; when interpolations split the content
((command_literal) @injection.content
 (#set! injection.language "bash")
 (#set! injection.include-children))
