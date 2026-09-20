import { LegalDoc, type LegalSection } from "./legal-doc";

// 隐私政策占位骨架（差异清单 #113）：章节结构与差异清单约定一致，正文待法务确认。
const SECTIONS: LegalSection[] = [
    {
        heading: "我们收集的信息",
        paragraphs: [
            "占位条款：注册与使用过程中收集的信息范围，包括账号资料、生成记录与必要的设备日志信息。",
        ],
    },
    {
        heading: "信息的使用",
        paragraphs: [
            "占位条款：收集信息的使用目的，包括提供服务、账号安全、内容审核与必要的运营统计。",
        ],
    },
    {
        heading: "信息的存储",
        paragraphs: [
            "占位条款：信息与生成产物的存储位置、保留期限，以及当前浏览器本地保存与云端存储的范围说明。",
        ],
    },
    {
        heading: "用户权利",
        paragraphs: [
            "占位条款：用户查询、更正、删除个人信息的权利，以及导出个人数据副本的方式。",
        ],
    },
    {
        heading: "账号注销与删除",
        paragraphs: [
            "占位条款：注销申请后的冷静期安排，注销生效时个人资料匿名化、业务数据删除与依法保留的记录范围。",
        ],
    },
];

/** 隐私政策页入口：把约定章节交给共用法务骨架渲染。 */
export default function PrivacyPage() {
    return <LegalDoc title="隐私政策" sections={SECTIONS} />;
}
