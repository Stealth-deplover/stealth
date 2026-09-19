"use client";

import { useEffect, useRef } from "react";
import {
  autocompletion,
  type CompletionContext,
  type CompletionResult,
} from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { linter } from "@codemirror/lint";
import { EditorState } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { parseAdminLogQuery } from "./admin-log-query";

const completions = [
  { label: "service:", type: "keyword", detail: "service name" },
  { label: "level:", type: "keyword", detail: "severity" },
  { label: "trace_id:", type: "keyword", detail: "trace correlation" },
  { label: "message:", type: "keyword", detail: "message text" },
  { label: "ERROR", type: "constant", detail: "log level" },
  { label: "WARN", type: "constant", detail: "log level" },
  { label: "INFO", type: "constant", detail: "log level" },
  { label: "DEBUG", type: "constant", detail: "log level" },
];

function completionSource(context: CompletionContext): CompletionResult | null {
  const word = context.matchBefore(/[A-Za-z_][A-Za-z0-9_-]*:?/);
  if (!word || (word.from === word.to && !context.explicit)) return null;
  return {
    from: word.from,
    options: completions,
    validFor: /^[A-Za-z0-9_:-]*$/,
  };
}

export function AdminQueryEditor({
  value,
  onChange,
  onSubmit,
  invalid = false,
}: {
  value: string;
  onChange: (value: string) => void;
  onSubmit?: () => void;
  invalid?: boolean;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const valueRef = useRef(value);
  const changeRef = useRef(onChange);
  const submitRef = useRef(onSubmit);

  useEffect(() => {
    valueRef.current = value;
    changeRef.current = onChange;
    submitRef.current = onSubmit;
  }, [onChange, onSubmit, value]);

  useEffect(() => {
    if (!containerRef.current) return;
    const view = new EditorView({
      state: EditorState.create({
        doc: valueRef.current,
        extensions: [
          history(),
          EditorView.lineWrapping,
          keymap.of([
            {
              key: "Mod-Enter",
              run: () => {
                submitRef.current?.();
                return true;
              },
            },
            ...defaultKeymap,
            ...historyKeymap,
          ]),
          autocompletion({ override: [completionSource] }),
          linter((editor) =>
            parseAdminLogQuery(editor.state.doc.toString()).diagnostics.map(
              (diagnostic) => ({
                from: diagnostic.from,
                to: Math.max(diagnostic.to, diagnostic.from + 1),
                severity: "error",
                message: diagnostic.message,
              }),
            ),
          ),
          EditorView.theme({
            "&": {
              backgroundColor: "transparent",
              color: "#d0d6e0",
              minHeight: "46px",
            },
            ".cm-content": {
              padding: "12px 14px",
              fontFamily: '"JetBrains Mono", ui-monospace, monospace',
              fontSize: "12px",
              minHeight: "22px",
            },
            ".cm-line": { padding: "0" },
            ".cm-gutters": { display: "none" },
            "&.cm-focused": { outline: "none" },
            ".cm-tooltip": {
              backgroundColor: "#161718",
              border: "1px solid #383b3f",
              color: "#d0d6e0",
            },
            ".cm-tooltip-autocomplete ul li[aria-selected]": {
              backgroundColor: "#23252a",
              color: "#ffffff",
            },
          }),
          EditorView.updateListener.of((update) => {
            if (!update.docChanged) return;
            const next = update.state.doc.toString();
            valueRef.current = next;
            changeRef.current(next);
          }),
        ],
      }),
      parent: containerRef.current,
    });
    view.dom.setAttribute("aria-label", "Structured log query");
    view.dom.setAttribute("role", "textbox");
    viewRef.current = view;
    return () => {
      view.destroy();
      viewRef.current = null;
    };
  }, []);

  useEffect(() => {
    const view = viewRef.current;
    if (!view || view.state.doc.toString() === value) return;
    view.dispatch({
      changes: { from: 0, to: view.state.doc.length, insert: value },
    });
  }, [value]);

  return (
    <div
      ref={containerRef}
      className={`min-h-[46px] rounded-md border bg-carbon transition-colors focus-within:ring-2 focus-within:ring-acid-lime/15 ${invalid ? "border-coral-red/80" : "border-graphite focus-within:border-acid-lime/70"}`}
    />
  );
}
