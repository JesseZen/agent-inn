import { DialogSelect } from "../../ui/dialog-select"
import { useRoute } from "../../context/route"
import { useLanguage } from "../../context/language"

export function DialogSubagent(props: { sessionID: string }) {
  const route = useRoute()
  const { t } = useLanguage()

  return (
    <DialogSelect
      title={t("session.subagentActions")}
      options={[
        {
          title: t("session.open"),
          value: "subagent.view",
          description: t("session.subagentDescription"),
          onSelect: (dialog) => {
            route.navigate({
              type: "session",
              sessionID: props.sessionID,
            })
            dialog.clear()
          },
        },
      ]}
    />
  )
}
