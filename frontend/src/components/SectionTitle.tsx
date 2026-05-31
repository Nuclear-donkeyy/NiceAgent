import styles from "./SectionTitle.module.css";

interface SectionTitleProps {
  text: string;
}

export function SectionTitle({ text }: SectionTitleProps) {
  return <div className={styles.title}>{text}</div>;
}
