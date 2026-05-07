local M = {}

local function tartalo_bin()
  -- prefer a local ./tartalo binary (built in the project), fall back to PATH
  local local_bin = vim.fn.findfile('tartalo', vim.fn.getcwd() .. ';')
  if local_bin ~= '' then
    return vim.fn.fnamemodify(local_bin, ':p')
  end
  return 'tartalo'
end

local function run_in_terminal(cmd)
  vim.cmd('botright split | terminal ' .. cmd)
  vim.cmd('startinsert')
end

function M.run(file)
  file = file or vim.fn.expand('%:p')
  run_in_terminal(tartalo_bin() .. ' run ' .. vim.fn.shellescape(file))
end

function M.build(file)
  file = file or vim.fn.expand('%:p')
  run_in_terminal(tartalo_bin() .. ' build ' .. vim.fn.shellescape(file))
end

function M.fmt(file)
  file = file or vim.fn.expand('%:p')
  local result = vim.fn.systemlist(tartalo_bin() .. ' fmt -w ' .. vim.fn.shellescape(file))
  if vim.v.shell_error ~= 0 then
    vim.notify(table.concat(result, '\n'), vim.log.levels.ERROR, { title = 'tartalo fmt' })
  else
    -- reload the buffer so edits are visible
    vim.cmd('silent! edit!')
    vim.notify('Formatted ' .. vim.fn.fnamemodify(file, ':t'), vim.log.levels.INFO, { title = 'tartalo fmt' })
  end
end

function M.test(path)
  path = path or vim.fn.expand('%:p')
  run_in_terminal(tartalo_bin() .. ' test ' .. vim.fn.shellescape(path))
end

function M.check(file)
  file = file or vim.fn.expand('%:p')
  local output = vim.fn.systemlist(tartalo_bin() .. ' check ' .. vim.fn.shellescape(file))
  if vim.v.shell_error ~= 0 then
    vim.notify(table.concat(output, '\n'), vim.log.levels.ERROR, { title = 'tartalo check' })
  else
    vim.notify('OK', vim.log.levels.INFO, { title = 'tartalo check' })
  end
end

return M
