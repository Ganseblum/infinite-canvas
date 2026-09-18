import { Alert, Tag, Typography } from "antd";

export type LegalSection = {
    heading: string;
    paragraphs: string[];
};

// 条款与隐私页共用骨架：静态排版，每节标注占位状态（差异清单 #113）。
// 正文是占位骨架，正式法务文本确认后整体替换，不做中英文案。
export function LegalDoc({ title, sections }: { title: string; sections: LegalSection[] }) {
    return (
        <main className="min-h-dvh overflow-y-auto bg-background px-4 py-12 text-stone-950 dark:text-stone-100">
            <div className="mx-auto max-w-3xl">
                <Typography.Title level={2} className="!mb-2">
                    {title}
                </Typography.Title>
                <Alert type="warning" showIcon message="本文为占位内容，正式文本待法务确认后发布。" />
                <div className="mt-10 flex flex-col gap-10">
                    {sections.map((section, index) => (
                        <section key={section.heading}>
                            <Typography.Title level={4} className="!mb-3 !mt-0">
                                {index + 1}. {section.heading}
                                <Tag className="ml-3 align-middle" color="warning">
                                    占位内容，待法务确认
                                </Tag>
                            </Typography.Title>
                            {section.paragraphs.map((paragraph) => (
                                <Typography.Paragraph key={paragraph} className="!mb-2 text-stone-600 dark:text-stone-300">
                                    {paragraph}
                                </Typography.Paragraph>
                            ))}
                        </section>
                    ))}
                </div>
            </div>
        </main>
    );
}
