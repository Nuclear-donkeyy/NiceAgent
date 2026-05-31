import styles from "./Empty.module.css";

interface EmptyProps {
  text: string;
}

export function Empty({ text }: EmptyProps) {
  return <div className={styles.empty}>{text}</div>;
}
