export function byID<T extends HTMLElement = HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`missing required element #${id}`);
  return node as T;
}

export function element<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className = "",
  text?: string | number,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = String(text);
  return node;
}

export function replaceChildren(node: HTMLElement, children: Node[]): void {
  node.replaceChildren(...children);
}
