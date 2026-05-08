import { EditorView } from "@codemirror/view";
import {
  HighlightStyle,
  syntaxHighlighting,
} from "@codemirror/language";
import { tags as t } from "@lezer/highlight";
import type { Extension } from "@codemirror/state";

const ember = "#ff7a3d";
const sun = "#ffb547";
const leaf = "#b6e08a";
const sky = "#6cc5ff";
const violet = "#c896ff";
const fg = "#f1f1ef";
const fgMuted = "#8a8a85";
const fgSubtle = "#5a5a55";
const bg = "#08080a";
const bgGutter = "#0c0c0e";
const lineActive = "rgba(255, 122, 61, 0.05)";
const selection = "rgba(255, 181, 71, 0.18)";
const matchBracket = "rgba(255, 122, 61, 0.22)";
const errorRed = "#ff6a6a";
const warningAmber = "#ffb547";

export const tartaloTheme = EditorView.theme(
  {
    "&": {
      color: fg,
      backgroundColor: bg,
      height: "100%",
      fontSize: "0.88rem",
    },

    ".cm-scroller": {
      fontFamily: '"JetBrains Mono", ui-monospace, monospace',
      lineHeight: "1.6",
    },

    ".cm-content": {
      caretColor: ember,
      padding: "0.6rem 0",
    },

    ".cm-cursor, .cm-dropCursor": { borderLeftColor: ember },

    "&.cm-focused .cm-cursor": { borderLeftColor: ember },

    "&.cm-focused .cm-selectionBackground, ::selection, .cm-selectionBackground":
      { backgroundColor: selection },

    ".cm-gutters": {
      backgroundColor: bgGutter,
      color: fgSubtle,
      borderRight: "1px solid rgba(255, 255, 255, 0.06)",
    },

    ".cm-activeLineGutter": {
      backgroundColor: "transparent",
      color: ember,
    },

    ".cm-activeLine": { backgroundColor: lineActive },

    ".cm-lineNumbers .cm-gutterElement": {
      padding: "0 0.7rem 0 0.5rem",
      minWidth: "1.8rem",
    },

    ".cm-foldGutter .cm-gutterElement": {
      color: fgSubtle,
      cursor: "pointer",
    },
    ".cm-foldGutter .cm-gutterElement:hover": { color: ember },

    ".cm-matchingBracket, .cm-nonmatchingBracket": {
      backgroundColor: matchBracket,
      color: fg,
      outline: "none",
    },

    // Lint diagnostic squigglies
    ".cm-lintRange-error": {
      backgroundImage:
        `url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 6 3'><path d='M0 3 L1.5 1.5 L3 3 L4.5 1.5 L6 3' fill='none' stroke='${encodeURIComponent(errorRed)}' stroke-width='0.7'/></svg>")`,
      backgroundRepeat: "repeat-x",
      backgroundPosition: "left bottom",
    },
    ".cm-lintRange-warning": {
      backgroundImage:
        `url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 6 3'><path d='M0 3 L1.5 1.5 L3 3 L4.5 1.5 L6 3' fill='none' stroke='${encodeURIComponent(warningAmber)}' stroke-width='0.7'/></svg>")`,
      backgroundRepeat: "repeat-x",
      backgroundPosition: "left bottom",
    },
    ".cm-diagnostic-error": { borderLeft: `3px solid ${errorRed}` },
    ".cm-diagnostic-warning": { borderLeft: `3px solid ${warningAmber}` },
    ".cm-tooltip-lint": {
      backgroundColor: "#15151a",
      border: "1px solid rgba(255, 255, 255, 0.1)",
      color: fg,
      fontFamily: '"JetBrains Mono", ui-monospace, monospace',
      fontSize: "0.78rem",
    },
    ".cm-panels": {
      backgroundColor: "#10101a",
      color: fg,
    },
    ".cm-panel.cm-panel-lint": {
      backgroundColor: "#10101a",
      borderTop: "1px solid rgba(255, 255, 255, 0.08)",
      fontFamily: '"JetBrains Mono", ui-monospace, monospace',
      fontSize: "0.78rem",
    },

    ".cm-tooltip": {
      backgroundColor: "#15151a",
      border: "1px solid rgba(255, 255, 255, 0.1)",
      color: fg,
    },

    // Search panel
    ".cm-panel.cm-search input": {
      backgroundColor: "#1a1a1f",
      color: fg,
      border: "1px solid rgba(255, 255, 255, 0.1)",
      borderRadius: "4px",
      padding: "0.2rem 0.4rem",
    },
    ".cm-panel.cm-search button": {
      backgroundColor: "transparent",
      color: fgMuted,
      border: "1px solid rgba(255, 255, 255, 0.1)",
      borderRadius: "4px",
      cursor: "pointer",
    },
  },
  { dark: true }
);

// Highlight style for shell output (paired with the legacy-modes shell mode).
const shHighlight = HighlightStyle.define([
  { tag: t.keyword, color: ember, fontWeight: "500" },
  { tag: t.string, color: leaf },
  { tag: t.comment, color: "#6a6a65", fontStyle: "italic" },
  { tag: t.number, color: violet },
  { tag: [t.variableName, t.special(t.variableName)], color: sun },
  { tag: t.atom, color: sky },
  { tag: t.operator, color: "#c0c0bc" },
  { tag: t.definition(t.variableName), color: fg },
]);

export const shHighlighting: Extension = syntaxHighlighting(shHighlight);
