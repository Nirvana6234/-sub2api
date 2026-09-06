import type { Metadata } from "next";
import { getBuildConfig } from "@/config/build";
import { PawPwaRegister } from "./PawPwaRegister";
import "./globals.css";

const buildConfig = getBuildConfig();
const publicPath = (name: string) => `${buildConfig.mountPath}/${name}`;

export const metadata: Metadata = {
  title: "共飞AI工作台",
  description: "sub2api 的 Chat 中文多端应用",
  manifest: publicPath("manifest.webmanifest"),
  icons: {
    icon: publicPath("paw-icon.svg"),
    apple: publicPath("paw-icon.svg"),
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="zh-CN">
      <body>
        <PawPwaRegister />
        <script
          dangerouslySetInnerHTML={{
            __html: `window.__PAW_CONFIG__=${JSON.stringify(buildConfig)};`,
          }}
        />
        {children}
      </body>
    </html>
  );
}
