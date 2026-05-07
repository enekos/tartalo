local M = {}

local defaults = {
  -- keymaps applied in tartalo buffers; set to false to disable all
  keymaps = {
    run   = '<leader>tr',
    build = '<leader>tb',
    fmt   = '<leader>tf',
    test  = '<leader>tt',
    check = '<leader>tc',
  },
  -- auto-format on save
  format_on_save = false,
  -- enable tree-sitter (requires :TSInstall tartalo after first load)
  treesitter = true,
  -- enable built-in LSP via `tartalo lsp`
  lsp = true,
}

-- Resolve the tartalo binary: prefer a local build, fall back to PATH.
local function tartalo_bin()
  local local_bin = vim.fn.findfile('tartalo', vim.fn.expand('~') .. '/tartalo;')
  if local_bin ~= '' then
    return vim.fn.fnamemodify(local_bin, ':p')
  end
  return 'tartalo'
end

local function register_treesitter(plugin_dir)
  local ok, parsers = pcall(require, 'nvim-treesitter.parsers')
  if not ok then return end

  local cfg = parsers.get_parser_configs()
  cfg.tartalo = {
    install_info = {
      url           = vim.fn.expand('~/tartalo/tree-sitter-tartalo'),
      files         = { 'src/parser.c' },
      generate_requires_npm        = false,
      requires_generate_from_grammar = false,
    },
    filetype = 'tartalo',
  }
end

local function start_lsp(buf)
  local bin = tartalo_bin()
  if vim.fn.executable(bin) == 0 then return end

  vim.lsp.start({
    name      = 'tartalo',
    cmd       = { bin, 'lsp' },
    root_dir  = vim.fn.getcwd(),
    filetypes = { 'tartalo' },
  }, { bufnr = buf })
end

local function apply_keymaps(buf, maps)
  local cmd = require('tartalo.commands')
  local opts = { buffer = buf, silent = true }
  local bindings = {
    { maps.run,   cmd.run,   'Tartalo: run file' },
    { maps.build, cmd.build, 'Tartalo: build file' },
    { maps.fmt,   cmd.fmt,   'Tartalo: format file' },
    { maps.test,  cmd.test,  'Tartalo: run tests' },
    { maps.check, cmd.check, 'Tartalo: type-check file' },
  }
  for _, b in ipairs(bindings) do
    if b[1] then
      vim.keymap.set('n', b[1], b[2], vim.tbl_extend('force', opts, { desc = b[3] }))
    end
  end
end

function M.setup(opts)
  local cfg = vim.tbl_deep_extend('force', defaults, opts or {})

  if cfg.treesitter then
    -- plugin_dir is the runtime path entry (this file lives at lua/tartalo/init.lua)
    local plugin_dir = vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':h:h:h')
    register_treesitter(plugin_dir)
  end

  local aug = vim.api.nvim_create_augroup('TartaloPlugin', { clear = true })

  vim.api.nvim_create_autocmd('FileType', {
    pattern  = 'tartalo',
    group    = aug,
    callback = function(ev)
      if cfg.keymaps then
        apply_keymaps(ev.buf, cfg.keymaps)
      end
      if cfg.lsp then
        start_lsp(ev.buf)
      end
      if cfg.format_on_save then
        vim.api.nvim_create_autocmd('BufWritePre', {
          buffer   = ev.buf,
          callback = function() require('tartalo.commands').fmt() end,
        })
      end
    end,
  })

  -- User commands (work from any filetype, act on current file)
  vim.api.nvim_create_user_command('TtRun',   function() require('tartalo.commands').run() end,   { desc = 'tartalo run' })
  vim.api.nvim_create_user_command('TtBuild', function() require('tartalo.commands').build() end, { desc = 'tartalo build' })
  vim.api.nvim_create_user_command('TtFmt',   function() require('tartalo.commands').fmt() end,   { desc = 'tartalo fmt' })
  vim.api.nvim_create_user_command('TtTest',  function() require('tartalo.commands').test() end,  { desc = 'tartalo test' })
  vim.api.nvim_create_user_command('TtCheck', function() require('tartalo.commands').check() end, { desc = 'tartalo check' })
end

return M
