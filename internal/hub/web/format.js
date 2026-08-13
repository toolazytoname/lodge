export const exposureLabel = {
    public: "公网",
    tailnet: "Tailnet",
    local: "本机",
    other: "待确认",
    none: "无监听",
};
export const exposureOrder = {
    public: 0,
    other: 1,
    tailnet: 2,
    local: 3,
    none: 4,
};
export const pageMeta = {
    overview: { eyebrow: "FLEET OVERVIEW", title: "全局状态" },
    hosts: { eyebrow: "HOST INVENTORY", title: "主机目录" },
    services: { eyebrow: "SERVICE CATALOG", title: "服务目录" },
    security: { eyebrow: "SECURITY POSTURE", title: "安全态势" },
    operations: { eyebrow: "CONTROLLED ACTIONS", title: "运维中心" },
};
export function isPageID(value) {
    return value in pageMeta;
}
export function safeWebURL(raw) {
    if (!raw)
        return null;
    try {
        const parsed = new URL(raw);
        const webScheme = parsed.protocol === "http:" || parsed.protocol === "https:";
        return webScheme && !parsed.username && !parsed.password ? parsed.href : null;
    }
    catch {
        return null;
    }
}
export function serviceExposure(service) {
    return (service.ports ?? []).length ? service.maxExposure : "none";
}
export function serviceDisplayName(service) {
    return service.alias?.trim() || service.name;
}
export function serviceRuntime(service) {
    if (service.composeProject) {
        return `${service.composeProject}/${service.composeService || service.name}`;
    }
    return service.kind;
}
export function isFailed(service) {
    const status = service.status.toLowerCase();
    const health = (service.health ?? "").toLowerCase();
    return status.includes("failed") || status.includes("dead") || health === "unhealthy";
}
export function needsAttention(service) {
    return isFailed(service) || Boolean(service.unidentified);
}
export function formatLastSeen(raw) {
    if (!raw)
        return "尚未同步";
    const date = new Date(raw);
    if (Number.isNaN(date.getTime()))
        return raw;
    return new Intl.DateTimeFormat("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
    }).format(date);
}
export function actionKindLabel(kind) {
    const labels = {
        logs: "读取日志",
        start: "启动",
        restart: "重启",
        stop: "停止",
    };
    return labels[kind];
}
export function actionRiskLabel(risk) {
    const labels = {
        read: "只读",
        change: "状态变更",
        disruptive: "中断风险",
    };
    return labels[risk];
}
export function deploymentKindLabel(kind) {
    return kind === "rollback" ? "回滚" : "部署";
}
export function shortImageDigest(image) {
    const marker = "@sha256:";
    const offset = image.lastIndexOf(marker);
    if (offset < 0)
        return "摘要不可用";
    return `sha256:${image.slice(offset + marker.length, offset + marker.length + 12)}…`;
}
export function operationStateLabel(stateValue) {
    const labels = {
        requested: "已请求",
        running: "执行中",
        succeeded: "成功",
        failed: "失败",
        rolled_back: "已回滚",
    };
    return labels[stateValue];
}
export function operationKindLabel(kind) {
    const labels = {
        logs: "读取日志",
        start: "启动",
        restart: "重启",
        stop: "停止",
        deploy: "部署",
        rollback: "回滚",
    };
    return labels[kind];
}
