import { Boxes, CreditCard, FileText, ImagePlus, Images, Maximize2, Settings2, Video, Users, CalendarCheck } from "lucide-react";

export const navigationTools = [
    {
        slug: "canvas",
        icon: Maximize2,
    },
    {
        slug: "image",
        icon: ImagePlus,
    },
    {
        slug: "video",
        icon: Video,
    },
    {
        slug: "models",
        icon: Boxes,
    },
    {
        slug: "prompts",
        icon: FileText,
    },
    {
        slug: "community",
        icon: Users,
    },
    {
        slug: "activity",
        icon: CalendarCheck,
    },
    {
        slug: "assets",
        icon: Images,
    },
    {
        slug: "config",
        icon: Settings2,
    },
    {
        slug: "pricing",
        icon: CreditCard,
    },
] as const;

export type NavigationToolSlug = (typeof navigationTools)[number]["slug"];
