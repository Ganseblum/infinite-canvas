import { useCallback, useEffect, useMemo, useState } from "react";
import { App } from "antd";
import { useTranslation } from "react-i18next";
import { APP_VERSION } from "@/constant/env";
import { parseChangelog, type ReleaseInfo } from "@/lib/release";

// 远端版本与更新日志都直接取 GitHub raw 文件：本地构建产物里也内置一份清单作离线兜底。
const latestVersionUrl = "https://raw.githubusercontent.com/basketikun/infinite-canvas/main/VERSION";
const latestChangelogUrl = "https://raw.githubusercontent.com/basketikun/infinite-canvas/main/CHANGELOG.md";

/** 读取构建时注入的内置发布清单（vite define 的 __APP_RELEASES__）。 */

function readLocalReleases(): ReleaseInfo[] {
    return __APP_RELEASES__ || [];
}

function toVersionParts(version: string) {
    const match = version.trim().match(/^v?(\d+)\.(\d+)\.(\d+)/);
    return match ? match.slice(1).map(Number) : null;
}

// 语义化版本比较：逐段比较，前缀段相等才看下一段，任一段更大即更新。
function isNewerVersion(latestVersion: string, currentVersion: string) {
    const latest = toVersionParts(latestVersion);
    const current = toVersionParts(currentVersion);
    if (!latest || !current) return false;
    return latest.some((value, index) => value > current[index] && latest.slice(0, index).every((part, prevIndex) => part === current[prevIndex]));
}

/**
 * 版本检查与更新日志 hook：挂载时静默拉一次最新版本号判断 hasNewVersion，
 * 打开弹窗时再完整拉取 changelog；失败时回退到构建内置清单。
 */
export function useVersionCheck() {
    const { t } = useTranslation();
    const currentVersion = APP_VERSION;
    const { message } = App.useApp();
    const localReleases = useMemo(readLocalReleases, []);
    const [latestVersion, setLatestVersion] = useState(currentVersion);
    const [releases, setReleases] = useState<ReleaseInfo[]>(localReleases);
    const [checking, setChecking] = useState(false);
    const [open, setOpen] = useState(false);
    const hasNewVersion = isNewerVersion(latestVersion, currentVersion);

    const checkLatestVersion = useCallback(async () => {
        try {
            const response = await fetch(latestVersionUrl);
            if (!response.ok) return false;
            const version = await response.text();
            setLatestVersion(version.trim() || currentVersion);
            return true;
        } catch {
            return false;
        }
    }, [currentVersion]);

    const checkLatestRelease = useCallback(
        async (showMessage = false) => {
            setChecking(true);
            try {
                const [versionResponse, changelogResponse] = await Promise.all([fetch(latestVersionUrl), fetch(latestChangelogUrl)]);
                if (!versionResponse.ok) throw new Error(t("version.readFailed"));
                if (!changelogResponse.ok) throw new Error(t("version.changelogFailed"));
                const [version, changelog] = await Promise.all([versionResponse.text(), changelogResponse.text()]);
                setLatestVersion(version.trim() || currentVersion);
                if (changelog.trim()) setReleases(parseChangelog(changelog));
                if (showMessage) message.success(t("version.updated"));
                return true;
            } catch {
                setLatestVersion(currentVersion);
                setReleases(localReleases);
                if (showMessage) message.error(t("version.updateFailed"));
                return false;
            } finally {
                setChecking(false);
            }
        },
        [currentVersion, localReleases, message, t],
    );

    useEffect(() => {
        void checkLatestVersion();
    }, [checkLatestVersion]);

    const openReleaseModal = useCallback(() => {
        setOpen(true);
        void checkLatestRelease();
    }, [checkLatestRelease]);

    return {
        open,
        setOpen,
        openReleaseModal,
        latestVersion,
        releases,
        checking,
        hasNewVersion,
        checkLatestRelease,
    };
}
