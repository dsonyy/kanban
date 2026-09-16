// The only module that knows which markdown editor library is used. Swap the library here, keep the interface.
import { Crepe } from "@milkdown/crepe";
import { replaceAll } from "@milkdown/kit/utils";
import "@milkdown/crepe/theme/common/style.css";
import "@milkdown/crepe/theme/frame-dark.css";

export interface MarkdownEditor {
  getValue(): string;
  setValue(markdown: string): void;
  focus(): void;
  destroy(): Promise<void>;
}

export interface MarkdownEditorOptions {
  value?: string;
  placeholder?: string;
  onChange?: (markdown: string) => void;
}

export async function mountMarkdownEditor(root: HTMLElement, options: MarkdownEditorOptions = {}): Promise<MarkdownEditor> {
  const crepe = new Crepe({
    root,
    defaultValue: options.value ?? "",
    features: {
      [Crepe.Feature.TopBar]: false,
      [Crepe.Feature.AI]: false,
      [Crepe.Feature.ImageBlock]: false,
      [Crepe.Feature.Latex]: false,
    },
    featureConfigs: {
      [Crepe.Feature.Placeholder]: { text: options.placeholder ?? "", mode: "doc" },
    },
  });
  if (options.onChange) crepe.on((listener) => listener.markdownUpdated((_, markdown) => options.onChange(markdown)));
  await crepe.create();
  return {
    getValue: () => crepe.getMarkdown(),
    setValue: (markdown) => crepe.editor.action(replaceAll(markdown)),
    focus: () => root.querySelector<HTMLElement>("[contenteditable]")?.focus(),
    destroy: async () => {
      await crepe.destroy();
    },
  };
}
