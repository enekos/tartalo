use zed_extension_api::{self as zed, LanguageServerId, Result};

struct TartaloExtension;

impl zed::Extension for TartaloExtension {
    fn new() -> Self {
        Self
    }

    fn language_server_command(
        &mut self,
        _language_server_id: &LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<zed::Command> {
        let path = worktree.which("tartalo").ok_or_else(|| {
            "tartalo not found on PATH. Build it with: go build -o tartalo ./cmd/tartalo".to_string()
        })?;

        Ok(zed::Command {
            command: path,
            args: vec!["lsp".to_string()],
            env: Default::default(),
        })
    }
}

zed::register_extension!(TartaloExtension);
