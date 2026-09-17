import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "antd/dist/reset.css";
import "@admin/styles/globals.css";
import { RouterProvider } from "react-router-dom";

import { AppProviders } from "@admin/app-providers";
import "@admin/i18n";
import { router } from "@admin/router";

// 与主站保持同一套字体栈（主站在 main.tsx 里同样设置）。
document.body.style.fontFamily = '"SF Pro Display","SF Pro Text","PingFang SC","Microsoft YaHei","Helvetica Neue",sans-serif';

createRoot(document.getElementById("root")!).render(
    <StrictMode>
        <AppProviders>
            <RouterProvider router={router} />
        </AppProviders>
    </StrictMode>,
);
