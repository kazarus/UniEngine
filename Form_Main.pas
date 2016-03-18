unit Form_Main;

interface

uses
  System.SysUtils, System.Types, System.UITypes, System.Classes, System.Variants,
  FMX.Types, FMX.Controls, FMX.Forms, FMX.Graphics, FMX.Dialogs, FMX.TMSToolBar,
  FMX.StdCtrls, FMX.Controls.Presentation, System.Rtti, FMX.Grid, FMX.Layouts,
  FMX.TMSBaseControl, FMX.TMSGridCell, FMX.TMSGridOptions, FMX.TMSGridData,
  FMX.TMSCustomGrid, FMX.TMSGrid, FMX.TabControl;

type
  TFormMain = class(TForm)
    Tool_Main: TToolBar;
    Btnx_1: TButton;
    Btnx_2: TButton;
    spl1: TSplitter;
    Panl_1: TPanel;
    procedure FormShow(Sender: TObject);
    procedure Grid_1Click(Sender: TObject);
    procedure Btnx_1Click(Sender: TObject);
  private
  public
  end;

var
  FormMain: TFormMain;

implementation

{$R *.fmx}

procedure TFormMain.Btnx_1Click(Sender: TObject);
begin
  //
end;

procedure TFormMain.FormShow(Sender: TObject);
var
  I:Integer;
begin
  {Self.Grid_1.RowCount:=1200;
  for I := 0 to 1100 do
  begin
    Self.Grid_1.Cells[1,I]:=Format('CELL%D',[I]);
    self.Grid_1.AddCheckBox(2,I,False);
  end;}
end;

procedure TFormMain.Grid_1Click(Sender: TObject);
begin
  //
end;

end.
