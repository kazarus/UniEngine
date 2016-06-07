unit Dialog_ListUniConfig;

interface

uses
  System.SysUtils, System.Types, System.UITypes, System.Classes, System.Variants,
  FMX.Types, FMX.Controls, FMX.Forms, FMX.Graphics, FMX.Dialogs, System.Rtti,
  FMX.Layouts, FMX.Grid, FMX.Controls.Presentation, FMX.StdCtrls, FMX.TMSToolBar;

type
  TDialogListUniConfig = class(TForm)
    StringGrid1: TStringGrid;
    Tool_1: TToolBar;
    CornerButton1: TCornerButton;
    Btnv_1: TButton;
    procedure Btnv_1Click(Sender: TObject);
  private
  public
  end;

var
  DialogListUniConfig: TDialogListUniConfig;

function ViewListUniConfig(var AObjt:TObject):Integer;

implementation

{$R *.fmx}

function ViewListUniConfig(var AObjt:TObject):Integer;
begin
  try
    DialogListUniConfig:=TDialogListUniConfig.Create(nil);
    Result:=DialogListUniConfig.ShowModal;
  finally
    FreeAndNil(DialogListUniConfig);
  end;
end;

procedure TDialogListUniConfig.Btnv_1Click(Sender: TObject);
begin
  //
end;

end.
