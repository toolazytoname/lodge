export function byID(id) {
    const node = document.getElementById(id);
    if (!node)
        throw new Error(`missing required element #${id}`);
    return node;
}
export function element(tag, className = "", text) {
    const node = document.createElement(tag);
    if (className)
        node.className = className;
    if (text !== undefined)
        node.textContent = String(text);
    return node;
}
export function replaceChildren(node, children) {
    node.replaceChildren(...children);
}
