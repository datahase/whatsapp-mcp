import type { Metadata } from "next";
import "./globals.css";
export const metadata: Metadata = {
  title: "WhatsApp MCP",
  description: "Manage your WhatsApp connections.",
  robots: { index: false, follow: false },
  icons: {
    icon: "/admin/brand/whatsapp-mcp-icon.png",
    apple: "/admin/brand/whatsapp-mcp-icon.png",
  },
};
export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
