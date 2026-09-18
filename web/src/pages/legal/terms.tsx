import { LegalDoc, type LegalSection } from "./legal-doc";

// 服务条款占位骨架（差异清单 #113）：章节结构与差异清单约定一致，正文待法务确认。
const SECTIONS: LegalSection[] = [
    {
        heading: "账号与注册",
        paragraphs: [
            "占位条款：账号注册条件、邮箱验证要求、登录凭据的保管责任，以及用户申请注销账号的权利与流程。",
        ],
    },
    {
        heading: "点数与充值",
        paragraphs: [
            "占位条款：点数的获取方式、使用范围、有效期与退还规则，充值服务的对价与发票事项。",
        ],
    },
    {
        heading: "内容规范",
        paragraphs: [
            "占位条款：用户生成与发布内容应遵守的法律法规与社区规范，平台的内容审核机制与违规处置方式。",
        ],
    },
    {
        heading: "知识产权",
        paragraphs: [
            "占位条款：用户生成内容的权利归属、对平台的授权范围，以及平台标识与服务的知识产权声明。",
        ],
    },
    {
        heading: "免责声明",
        paragraphs: [
            "占位条款：服务的可用性不承诺、AI 模型输出结果的局限性说明，以及责任限制条款。",
        ],
    },
];

export default function TermsPage() {
    return <LegalDoc title="服务条款" sections={SECTIONS} />;
}
